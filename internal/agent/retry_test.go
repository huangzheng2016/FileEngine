package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
)

// stubRetryEnv freezes the retry environment: config, jitter source, sleep
// recorder and the fleet-wide rate-limit streak. Restores everything on
// cleanup. Not parallel-safe by design (package-level knobs).
func stubRetryEnv(t *testing.T, cfg config.AgentConfig) *[]time.Duration {
	t.Helper()
	oldCfg := config.Get()
	oldSleep := retrySleep
	oldRand := retryRandFloat64
	oldStreak := rateLimitStreak.Load()

	sleeps := &[]time.Duration{}
	config.Update(&config.Config{Agent: cfg})
	retrySleep = func(_ context.Context, d time.Duration) { *sleeps = append(*sleeps, d) }
	retryRandFloat64 = func() float64 { return 0.5 }
	rateLimitStreak.Store(0)

	t.Cleanup(func() {
		retrySleep = oldSleep
		retryRandFloat64 = oldRand
		rateLimitStreak.Store(oldStreak)
		if oldCfg != nil {
			config.Update(oldCfg)
		}
	})
	return sleeps
}

func TestClassifyRunError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want errorClass
	}{
		{"429 is rate_limit", &einoopenai.APIError{HTTPStatusCode: 429, Message: "throttled"}, errClassRateLimit},
		{"500 is server", &einoopenai.APIError{HTTPStatusCode: 500, Message: "boom"}, errClassServer},
		{"wrapped 503 is server", fmt.Errorf("agent run: %w", &einoopenai.APIError{HTTPStatusCode: 503}), errClassServer},
		{"400 is hard", &einoopenai.APIError{HTTPStatusCode: 400, Message: "bad request"}, errClassHard},
		{"401 is hard", &einoopenai.APIError{HTTPStatusCode: 401, Message: "invalid key"}, errClassHard},
		{"403 is hard", &einoopenai.APIError{HTTPStatusCode: 403, Message: "forbidden"}, errClassHard},
		{"context length is hard", &einoopenai.APIError{HTTPStatusCode: 400, Message: "context_length_exceeded: too many tokens"}, errClassHard},
		{"proxy 502 text is server", errors.New("error, status code: 502, status: 502 Bad Gateway, message: invalid character '<' looking for beginning of value, body: <html>..."), errClassServer},
		{"proxy 429 text is rate_limit", errors.New("error, status code: 429, status: 429 Too Many Requests, message: x, body: y"), errClassRateLimit},
		{"proxy 400 text is hard", errors.New("error, status code: 400, status: 400 Bad Request, message: x, body: y"), errClassHard},
		{"url timeout is network", &url.Error{Op: "Post", URL: "http://x/v1", Err: &net.DNSError{IsTimeout: true, Name: "x"}}, errClassNetwork},
		{"dns failure is network", &net.DNSError{Name: "x", IsNotFound: true}, errClassNetwork},
		{"deadline exceeded is network", context.DeadlineExceeded, errClassNetwork},
		{"wrapped deadline is network", fmt.Errorf("run: %w", context.DeadlineExceeded), errClassNetwork},
		{"canceled is hard", context.Canceled, errClassHard},
		{"unknown stays retryable", errors.New("something odd"), errClassServer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyRunError(tc.err); got != tc.want {
				t.Fatalf("classifyRunError(%v): got %s, want %s", tc.err, got, tc.want)
			}
		})
	}
}

func TestParseRetryAfterMessage(t *testing.T) {
	cases := []struct {
		msg  string
		want time.Duration
	}{
		{"rate limit reached, please retry after 7 seconds", 7 * time.Second},
		{"Retry-After: 15", 15 * time.Second},
		{"retry after 500ms", 500 * time.Millisecond},
		{"please try again in 2 minutes", 2 * time.Minute},
		{"try again in 3s", 3 * time.Second},
		{"rate limit exceeded", 0},
		{"", 0},
	}
	for _, tc := range cases {
		if got := parseRetryAfterMessage(tc.msg); got != tc.want {
			t.Errorf("parseRetryAfterMessage(%q): got %v, want %v", tc.msg, got, tc.want)
		}
	}
}

type fakeRetryAfterCarrier struct{ d time.Duration }

func (e *fakeRetryAfterCarrier) Error() string        { return "carrier" }
func (e *fakeRetryAfterCarrier) RetryAfter() time.Duration { return e.d }

func TestRetryAfterFromError(t *testing.T) {
	if got := retryAfterFromError(&einoopenai.APIError{HTTPStatusCode: 429, Message: "please retry after 9 seconds"}); got != 9*time.Second {
		t.Fatalf("api error message hint: got %v, want 9s", got)
	}
	if got := retryAfterFromError(fmt.Errorf("wrap: %w", &fakeRetryAfterCarrier{d: 4 * time.Second})); got != 4*time.Second {
		t.Fatalf("carrier interface: got %v, want 4s", got)
	}
	if got := retryAfterFromError(errors.New("plain")); got != 0 {
		t.Fatalf("no hint: got %v, want 0", got)
	}
}

func TestMaxRetriesForClass(t *testing.T) {
	cfg := &config.AgentConfig{MaxRetries: 3, RateLimitMaxRetries: 8}
	if got := maxRetriesForClass(errClassRateLimit, cfg); got != 8 {
		t.Fatalf("rate_limit budget: got %d, want 8", got)
	}
	if got := maxRetriesForClass(errClassServer, cfg); got != 3 {
		t.Fatalf("server budget: got %d, want 3", got)
	}
	if got := maxRetriesForClass(errClassNetwork, cfg); got != 3 {
		t.Fatalf("network budget: got %d, want 3", got)
	}
	if got := maxRetriesForClass(errClassHard, cfg); got != 0 {
		t.Fatalf("hard budget: got %d, want 0", got)
	}
	// Zero-value config falls back to built-in defaults.
	zero := &config.AgentConfig{}
	if got := maxRetriesForClass(errClassRateLimit, zero); got != 8 {
		t.Fatalf("rate_limit default budget: got %d, want 8", got)
	}
	if got := maxRetriesForClass(errClassServer, zero); got != 3 {
		t.Fatalf("server default budget: got %d, want 3", got)
	}
}

func TestBackoffDelaySequence(t *testing.T) {
	stubRetryEnv(t, config.AgentConfig{RetryBaseDelay: 2, RetryMaxDelay: 60})
	cfg := retryCfg()

	// jitter 0.5 → delay = raw/2; raw doubles from base 2s up to the 60s cap.
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for attempt := 1; attempt <= len(want); attempt++ {
		got := backoffDelay(errClassServer, attempt, 0, cfg)
		if got != want[attempt-1]*time.Second {
			t.Fatalf("attempt %d: got %v, want %v", attempt, got, want[attempt-1]*time.Second)
		}
	}
	// Huge attempt numbers must not overflow the shift.
	if got := backoffDelay(errClassServer, 60, 0, cfg); got != 30*time.Second {
		t.Fatalf("attempt 60: got %v, want 30s (capped)", got)
	}
	// Full jitter bounds: rand 0 → 0, rand →1 approaches raw.
	retryRandFloat64 = func() float64 { return 0 }
	if got := backoffDelay(errClassServer, 3, 0, cfg); got != 0 {
		t.Fatalf("jitter 0: got %v, want 0", got)
	}
	retryRandFloat64 = func() float64 { return 0.999 }
	if got := backoffDelay(errClassServer, 3, 0, cfg); got <= 7*time.Second || got >= 8*time.Second {
		t.Fatalf("jitter ~1: got %v, want in (7s, 8s)", got)
	}
}

func TestBackoffDelayHonorsRetryAfter(t *testing.T) {
	stubRetryEnv(t, config.AgentConfig{RetryBaseDelay: 2, RetryMaxDelay: 60})
	cfg := retryCfg()

	// Retry-After replaces the computed wait: 7s + jittered base slot (1s).
	if got := backoffDelay(errClassRateLimit, 1, 7*time.Second, cfg); got != 8*time.Second {
		t.Fatalf("retry-after 7s: got %v, want 8s", got)
	}
	// Absurd hints are clamped to 4*maxDelay.
	if got := backoffDelay(errClassRateLimit, 1, time.Hour, cfg); got != 241*time.Second {
		t.Fatalf("retry-after 1h: got %v, want 241s (clamped)", got)
	}
	// Retry-After only applies to rate limiting.
	if got := backoffDelay(errClassServer, 1, 7*time.Second, cfg); got != time.Second {
		t.Fatalf("server class ignores hint: got %v, want 1s", got)
	}
}

func TestBackoffDelayRateLimitStreakBoost(t *testing.T) {
	stubRetryEnv(t, config.AgentConfig{RetryBaseDelay: 2, RetryMaxDelay: 60})
	cfg := retryCfg()

	// streak 0/1: no boost (attempt 1 raw = 2s, jitter 0.5 → 1s).
	if got := backoffDelay(errClassRateLimit, 1, 0, cfg); got != time.Second {
		t.Fatalf("streak 0: got %v, want 1s", got)
	}
	rateLimitStreak.Store(1)
	if got := backoffDelay(errClassRateLimit, 1, 0, cfg); got != time.Second {
		t.Fatalf("streak 1: got %v, want 1s", got)
	}
	// streak 3: +2 exponent slots → raw 8s → 4s.
	rateLimitStreak.Store(3)
	if got := backoffDelay(errClassRateLimit, 1, 0, cfg); got != 4*time.Second {
		t.Fatalf("streak 3: got %v, want 4s", got)
	}
	// boost is capped at 4 extra slots → raw 32s → 16s, however high streak goes.
	rateLimitStreak.Store(100)
	if got := backoffDelay(errClassRateLimit, 1, 0, cfg); got != 16*time.Second {
		t.Fatalf("streak 100: got %v, want 16s (boost capped)", got)
	}
}

// fakeAttempts fails with errs[i-1] on the i-th call, then succeeds.
type fakeAttempts struct {
	errs  []error
	calls int
}

func (f *fakeAttempts) run(_ context.Context) (*schema.Message, error) {
	f.calls++
	if f.calls <= len(f.errs) {
		return nil, f.errs[f.calls-1]
	}
	return &schema.Message{Role: schema.Assistant, Content: "ok"}, nil
}

func runFake(t *testing.T, ctx context.Context, f *fakeAttempts) (*schema.Message, error) {
	t.Helper()
	logger := NewLogger(nil, 900, 1)
	tracker := newTokenTrackingModel(nil)
	return runWithRetry(ctx, f.run, logger, "test", tracker)
}

func TestRunWithRetrySuccessFirstTry(t *testing.T) {
	sleeps := stubRetryEnv(t, config.AgentConfig{MaxRetries: 3, RateLimitMaxRetries: 8, RetryBaseDelay: 2, RetryMaxDelay: 60})
	rateLimitStreak.Store(5) // a prior throttling episode must be forgotten
	f := &fakeAttempts{}
	msg, err := runFake(t, context.Background(), f)
	if err != nil || msg == nil || msg.Content != "ok" {
		t.Fatalf("got msg=%+v err=%v, want ok", msg, err)
	}
	if f.calls != 1 || len(*sleeps) != 0 {
		t.Fatalf("calls=%d sleeps=%v, want 1 call no sleep", f.calls, *sleeps)
	}
	if got := rateLimitStreak.Load(); got != 0 {
		t.Fatalf("streak after success: got %d, want 0", got)
	}
}

func TestRunWithRetry429ThenSuccess(t *testing.T) {
	sleeps := stubRetryEnv(t, config.AgentConfig{MaxRetries: 3, RateLimitMaxRetries: 8, RetryBaseDelay: 2, RetryMaxDelay: 60})
	f := &fakeAttempts{errs: []error{
		&einoopenai.APIError{HTTPStatusCode: 429, Message: "slow down"},
	}}
	msg, err := runFake(t, context.Background(), f)
	if err != nil || msg == nil {
		t.Fatalf("got msg=%+v err=%v, want success", msg, err)
	}
	if f.calls != 2 || len(*sleeps) != 1 {
		t.Fatalf("calls=%d sleeps=%v, want 2 calls 1 sleep", f.calls, *sleeps)
	}
	// streak 1 → no boost; attempt 1 raw 2s, jitter 0.5 → 1s.
	if (*sleeps)[0] != time.Second {
		t.Fatalf("sleep: got %v, want 1s", (*sleeps)[0])
	}
	if got := rateLimitStreak.Load(); got != 0 {
		t.Fatalf("streak after success: got %d, want 0", got)
	}
}

func TestRunWithRetry429ExhaustsLargerBudget(t *testing.T) {
	sleeps := stubRetryEnv(t, config.AgentConfig{MaxRetries: 1, RateLimitMaxRetries: 3, RetryBaseDelay: 2, RetryMaxDelay: 60})
	throttled := &einoopenai.APIError{HTTPStatusCode: 429, Message: "slow down"}
	f := &fakeAttempts{errs: []error{throttled, throttled, throttled, throttled}}
	_, err := runFake(t, context.Background(), f)
	if err == nil {
		t.Fatal("want failure after budget exhausted")
	}
	// 1 initial + 3 retries from rate_limit_max_retries, NOT max_retries=1.
	if f.calls != 4 || len(*sleeps) != 3 {
		t.Fatalf("calls=%d sleeps=%v, want 4 calls 3 sleeps", f.calls, *sleeps)
	}
	// Every classified 429 bumps the fleet streak (including the final,
	// unretried one); nothing reset it.
	if got := rateLimitStreak.Load(); got != 4 {
		t.Fatalf("streak: got %d, want 4", got)
	}
}

func TestRunWithRetry429RetryAfterSleep(t *testing.T) {
	sleeps := stubRetryEnv(t, config.AgentConfig{MaxRetries: 3, RateLimitMaxRetries: 8, RetryBaseDelay: 2, RetryMaxDelay: 60})
	f := &fakeAttempts{errs: []error{
		&einoopenai.APIError{HTTPStatusCode: 429, Message: "rate limit reached, please retry after 7 seconds"},
	}}
	if _, err := runFake(t, context.Background(), f); err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if len(*sleeps) != 1 {
		t.Fatalf("sleeps=%v, want 1", *sleeps)
	}
	// 7s hint + jittered base slot (0.5 * 2s).
	if (*sleeps)[0] != 8*time.Second {
		t.Fatalf("retry-after sleep: got %v, want 8s", (*sleeps)[0])
	}
}

func TestRunWithRetryHardErrorZeroRetries(t *testing.T) {
	sleeps := stubRetryEnv(t, config.AgentConfig{MaxRetries: 3, RateLimitMaxRetries: 8, RetryBaseDelay: 2, RetryMaxDelay: 60})
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"400", &einoopenai.APIError{HTTPStatusCode: 400, Message: "bad request"}},
		{"401", &einoopenai.APIError{HTTPStatusCode: 401, Message: "invalid api key"}},
		{"context length", &einoopenai.APIError{HTTPStatusCode: 400, Message: "context_length_exceeded"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeAttempts{errs: []error{tc.err, tc.err, tc.err, tc.err}}
			_, err := runFake(t, context.Background(), f)
			if !errors.Is(err, tc.err) {
				t.Fatalf("got err=%v, want the original %v", err, tc.err)
			}
			if f.calls != 1 || len(*sleeps) != 0 {
				t.Fatalf("calls=%d sleeps=%v, want 1 call no sleep", f.calls, *sleeps)
			}
		})
	}
}

func TestRunWithRetryServerAndNetworkBudget(t *testing.T) {
	sleeps := stubRetryEnv(t, config.AgentConfig{MaxRetries: 2, RateLimitMaxRetries: 8, RetryBaseDelay: 2, RetryMaxDelay: 60})

	server := &einoopenai.APIError{HTTPStatusCode: 500, Message: "boom"}
	f := &fakeAttempts{errs: []error{server, server, server, server}}
	if _, err := runFake(t, context.Background(), f); err == nil {
		t.Fatal("want failure after max_retries")
	}
	if f.calls != 3 || len(*sleeps) != 2 {
		t.Fatalf("server: calls=%d sleeps=%v, want 3 calls 2 sleeps", f.calls, *sleeps)
	}

	*sleeps = nil
	netErr := &url.Error{Op: "Post", URL: "http://x/v1", Err: &net.DNSError{IsTimeout: true, Name: "x"}}
	f = &fakeAttempts{errs: []error{netErr, netErr, netErr, netErr}}
	if _, err := runFake(t, context.Background(), f); err == nil {
		t.Fatal("want failure after max_retries")
	}
	if f.calls != 3 || len(*sleeps) != 2 {
		t.Fatalf("network: calls=%d sleeps=%v, want 3 calls 2 sleeps", f.calls, *sleeps)
	}
	// Network failures must not feed the rate-limit streak.
	if got := rateLimitStreak.Load(); got != 0 {
		t.Fatalf("streak: got %d, want 0", got)
	}
}

func TestRunWithRetryReclassifiesMidLoop(t *testing.T) {
	sleeps := stubRetryEnv(t, config.AgentConfig{MaxRetries: 3, RateLimitMaxRetries: 8, RetryBaseDelay: 2, RetryMaxDelay: 60})
	// Throttling turns into a hard 400: the loop must stop immediately
	// instead of burning the remaining 429 budget.
	f := &fakeAttempts{errs: []error{
		&einoopenai.APIError{HTTPStatusCode: 429, Message: "slow down"},
		&einoopenai.APIError{HTTPStatusCode: 400, Message: "bad request"},
		&einoopenai.APIError{HTTPStatusCode: 400, Message: "bad request"},
	}}
	_, err := runFake(t, context.Background(), f)
	if err == nil {
		t.Fatal("want failure")
	}
	if f.calls != 2 || len(*sleeps) != 1 {
		t.Fatalf("calls=%d sleeps=%v, want 2 calls 1 sleep", f.calls, *sleeps)
	}
}

func TestRunWithRetryAbortsOnContextCancel(t *testing.T) {
	sleeps := stubRetryEnv(t, config.AgentConfig{MaxRetries: 3, RateLimitMaxRetries: 8, RetryBaseDelay: 2, RetryMaxDelay: 60})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := NewLogger(nil, 900, 1)
	tracker := newTokenTrackingModel(nil)
	calls := 0
	_, err := runWithRetry(ctx, func(ctx context.Context) (*schema.Message, error) {
		calls++
		cancel() // user stop lands while the first failure is being handled
		return nil, &einoopenai.APIError{HTTPStatusCode: 500, Message: "boom"}
	}, logger, "test", tracker)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got err=%v, want context.Canceled", err)
	}
	if calls != 1 || len(*sleeps) != 0 {
		t.Fatalf("calls=%d sleeps=%v, want 1 call no sleep", calls, *sleeps)
	}
}

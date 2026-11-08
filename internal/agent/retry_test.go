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

func (e *fakeRetryAfterCarrier) Error() string             { return "carrier" }
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

func TestClassifyRunErrorWithStatusHint(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		statusHint int
		want       errorClass
	}{
		{"hint 429 classifies plain error", errors.New("boom"), 429, errClassRateLimit},
		{"hint 503 classifies plain error", errors.New("boom"), 503, errClassServer},
		{"hint 401 is hard", errors.New("boom"), 401, errClassHard},
		{"error-carried status beats hint", &einoopenai.APIError{HTTPStatusCode: 400, Message: "bad"}, 429, errClassHard},
		{"hint ignored when error has text status", errors.New("error, status code: 502, status: 502, message: x, body: y"), 429, errClassServer},
		{"zero hint falls back to old paths", errors.New("boom"), 0, errClassServer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyRunErrorWithStatus(tc.err, tc.statusHint); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}


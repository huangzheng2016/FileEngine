package agent

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
	modelfactory "FileEngine/internal/model"
)

var testRetryCfg = config.AgentConfig{MaxRetries: 2, RateLimitMaxRetries: 4, RetryBaseDelay: 2, RetryMaxDelay: 60}

func decide(ctx context.Context, attempt int, err error) *adk.RetryDecision {
	return shouldRetryModelCall(ctx, &adk.RetryContext{RetryAttempt: attempt, Err: err})
}

func TestShouldRetryModelCallBudgets(t *testing.T) {
	stubRetryEnv(t, testRetryCfg)
	ctx := context.Background()
	throttled := &einoopenai.APIError{HTTPStatusCode: 429, Message: "slow down"}
	server := &einoopenai.APIError{HTTPStatusCode: 500, Message: "boom"}
	netErr := &url.Error{Op: "Post", URL: "http://x/v1", Err: &net.DNSError{IsTimeout: true, Name: "x"}}

	// 429: retried within rate_limit_max_retries; first wait is 0.5 * 2s.
	if d := decide(ctx, 1, throttled); d == nil || !d.Retry || d.Backoff != time.Second {
		t.Fatalf("429 attempt 1: %+v", d)
	}
	if d := decide(ctx, 4, throttled); d == nil || !d.Retry {
		t.Fatalf("429 attempt 4 must retry (budget 4): %+v", d)
	}
	if d := decide(ctx, 5, throttled); d != nil {
		t.Fatalf("429 attempt 5 must give up: %+v", d)
	}
	if got := rateLimitStreak.Load(); got != 3 {
		t.Fatalf("streak = %d, want 3", got)
	}
	// A success resets the fleet streak.
	if d := shouldRetryModelCall(ctx, &adk.RetryContext{RetryAttempt: 1}); d != nil || rateLimitStreak.Load() != 0 {
		t.Fatalf("success: decision %+v streak %d", d, rateLimitStreak.Load())
	}

	for name, err := range map[string]error{"server": server, "network": netErr} {
		if d := decide(ctx, 2, err); d == nil || !d.Retry {
			t.Fatalf("%s attempt 2 must retry (max_retries 2): %+v", name, d)
		}
		if d := decide(ctx, 3, err); d != nil {
			t.Fatalf("%s attempt 3 must give up: %+v", name, d)
		}
	}

	for _, hard := range []error{
		&einoopenai.APIError{HTTPStatusCode: 400, Message: "context_length_exceeded"},
		&einoopenai.APIError{HTTPStatusCode: 401, Message: "invalid api key"},
	} {
		if d := decide(ctx, 1, hard); d != nil {
			t.Fatalf("hard error %v must not retry: %+v", hard, d)
		}
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if d := decide(cancelled, 1, server); d != nil {
		t.Fatalf("canceled run must not retry: %+v", d)
	}
}

func TestShouldRetryModelCallRetryAfter(t *testing.T) {
	stubRetryEnv(t, testRetryCfg)
	msgHint := &einoopenai.APIError{HTTPStatusCode: 429, Message: "rate limit reached, please retry after 7 seconds"}
	if d := decide(context.Background(), 1, msgHint); d == nil || d.Backoff != 8*time.Second {
		t.Fatalf("message hint: %+v, want 7s + 1s jitter slot", d)
	}

	// Transport hints classify a status-less error and set the wait; they are
	// consumed so the next request starts clean.
	ctx, hints := modelfactory.WithResponseHints(context.Background())
	hints.Record(429, 5*time.Second)
	if d := decide(ctx, 1, errors.New("boom")); d == nil || d.Backoff != 6*time.Second {
		t.Fatalf("header hint: %+v, want 6s", d)
	}
	if hints.StatusCode() != 0 || hints.RetryAfter() != 0 {
		t.Fatal("hints must be consumed by the decision")
	}
	hints.Record(401, 0)
	if d := decide(ctx, 1, errors.New("boom")); d != nil {
		t.Fatalf("hint-only 401 must not retry: %+v", d)
	}
}

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

// The outer loop replays a run only for network failures before any model
// response; everything else was already retried per request.
func TestRunWithRetryOnlyReplaysEarlyNetworkFailures(t *testing.T) {
	sleeps := stubRetryEnv(t, testRetryCfg)
	logger := NewLogger(900, 0, 1)
	netErr := &url.Error{Op: "Post", URL: "http://x/v1", Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}}

	f := &fakeAttempts{errs: []error{netErr, netErr}}
	if msg, err := runWithRetry(context.Background(), f.run, logger, "t"); err != nil || msg.Content != "ok" || f.calls != 3 || len(*sleeps) != 2 {
		t.Fatalf("early network: err=%v calls=%d sleeps=%v", err, f.calls, *sleeps)
	}

	f = &fakeAttempts{errs: []error{netErr, netErr, netErr}}
	if _, err := runWithRetry(context.Background(), f.run, logger, "t"); !errors.Is(err, netErr) || f.calls != 3 {
		t.Fatalf("network budget: err=%v calls=%d", err, f.calls)
	}

	late := &respondedError{err: netErr}
	f = &fakeAttempts{errs: []error{late}}
	_, err := runWithRetry(context.Background(), f.run, logger, "t")
	var responded *respondedError
	if f.calls != 1 || errors.As(err, &responded) || !errors.Is(err, netErr) {
		t.Fatalf("network after a response must not replay: err=%v calls=%d", err, f.calls)
	}

	for _, e := range []error{
		&einoopenai.APIError{HTTPStatusCode: 500, Message: "boom"},
		&einoopenai.APIError{HTTPStatusCode: 429, Message: "slow"},
		&einoopenai.APIError{HTTPStatusCode: 400, Message: "bad"},
	} {
		f = &fakeAttempts{errs: []error{e}}
		if _, err := runWithRetry(context.Background(), f.run, logger, "t"); err == nil || f.calls != 1 {
			t.Fatalf("%v must not replay the run: calls=%d", e, f.calls)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	_, err = runWithRetry(ctx, func(context.Context) (*schema.Message, error) {
		cancel()
		return nil, netErr
	}, logger, "t")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("user stop: err=%v", err)
	}
}

// flakyModel answers like scriptedModel but fails its second request once
// with a 429.
type flakyModel struct {
	scripted scriptedModel
	requests int
}

func (m *flakyModel) Generate(ctx context.Context, in []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	return nil, errors.New("not used")
}

func (m *flakyModel) Stream(ctx context.Context, in []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	m.requests++
	if m.requests == 2 {
		return nil, &einoopenai.APIError{HTTPStatusCode: 429, Message: "slow down"}
	}
	return m.scripted.Stream(ctx, in, opts...)
}

func (m *flakyModel) BindTools([]*schema.ToolInfo) error { return nil }

// A 429 on turn 2 retries only that request: turn 1 is not replayed.
func TestModelRetryIsPerRequest(t *testing.T) {
	setupToolTestRepo(t)
	stubRetryEnv(t, config.AgentConfig{MaxRetries: 2, RateLimitMaxRetries: 4, RetryBaseDelay: 1, RetryMaxDelay: 1})
	retryRandFloat64 = func() float64 { return 0 }

	model := &flakyModel{}
	logger := NewLogger(204, 1, 0)
	msg, err := runADKWithRetry(context.Background(), newEchoAgent(t, model, nil), []*schema.Message{{Role: schema.User, Content: "go"}}, logger, "t", newTokenTrackingModel(model))
	if err != nil || msg == nil || msg.Content != "done" {
		t.Fatalf("run: msg=%+v err=%v", msg, err)
	}
	if model.requests != 3 || model.scripted.calls != 2 {
		t.Fatalf("requests=%d answered=%d, want 3 requests (turn 2 retried once) and 2 answers", model.requests, model.scripted.calls)
	}
}

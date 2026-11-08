package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	modelfactory "FileEngine/internal/model"
)

// Retries happen per model request: the ADK ChatModelAgent re-issues only
// the failed request (ModelRetryConfig), so a 429 at turn 6 never replays
// turns 1-5. shouldRetryModelCall applies the class-aware policy (retry.go)
// to every failed request. The outer run-level loop (runWithRetry) only
// covers network failures that hit before the model answered at all.

// modelRetryConfig builds the per-request retry policy of an agent. The
// class budgets are enforced by shouldRetryModelCall; MaxRetries is the
// largest of them so ADK never cuts a class short.
func modelRetryConfig() *adk.ModelRetryConfig {
	cfg := retryCfg()
	return &adk.ModelRetryConfig{
		MaxRetries:  max(maxRetriesForClass(errClassRateLimit, cfg), maxRetriesForClass(errClassServer, cfg)),
		ShouldRetry: shouldRetryModelCall,
	}
}

// shouldRetryModelCall decides one failed model request: hard errors and
// caller cancellation fail the run, rate_limit/server/network errors retry
// within their class budget after a jittered backoff (Retry-After honored).
// The transport hints of the request are consumed here so they never leak
// into the classification of a later request.
func shouldRetryModelCall(ctx context.Context, rc *adk.RetryContext) *adk.RetryDecision {
	var statusHint int
	var retryAfter time.Duration
	if h := modelfactory.ResponseHintsFromContext(ctx); h != nil {
		statusHint, retryAfter = h.StatusCode(), h.RetryAfter()
		h.Record(0, 0)
	}
	if rc.Err == nil {
		rateLimitStreak.Store(0)
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	class := classifyRunErrorWithStatus(rc.Err, statusHint)
	if class == errClassRateLimit {
		rateLimitStreak.Add(1)
	}
	cfg := retryCfg()
	budget := maxRetriesForClass(class, cfg)
	if rc.RetryAttempt > budget {
		log.Printf("model request failed (%s), no retries left (%d): %v", class, budget, rc.Err)
		return nil
	}
	if retryAfter <= 0 {
		retryAfter = retryAfterFromError(rc.Err)
	}
	delay := max(backoffDelay(class, rc.RetryAttempt, retryAfter, cfg), time.Millisecond)
	log.Printf("model request retry %d/%d (%s) in %s: %v", rc.RetryAttempt, budget, class, delay.Round(time.Millisecond), rc.Err)
	return &adk.RetryDecision{Retry: true, Backoff: delay}
}

// respondedError marks a run that failed after the model had already
// answered at least once; such runs are never replayed.
type respondedError struct{ err error }

func (e *respondedError) Error() string { return e.err.Error() }
func (e *respondedError) Unwrap() error { return e.err }

// runAttemptFunc executes one full agent run under ctx.
type runAttemptFunc func(ctx context.Context) (*schema.Message, error)

// runWithRetry runs attempt and replays the whole run only for network
// failures that happened before any model response (connection refused,
// DNS, a dead proxy …), within the network budget; every other failure has
// already been retried per request inside the run. Each attempt carries a
// fresh ResponseHints store so request-level classification sees transport
// status codes. A final failure is reported on the lane as a system message.
func runWithRetry(ctx context.Context, attempt runAttemptFunc, logger *Logger, label string) (*schema.Message, error) {
	retries := 0
	for {
		attemptCtx, hints := modelfactory.WithResponseHints(ctx)
		result, err := attempt(attemptCtx)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		class := classifyRunErrorWithStatus(err, hints.StatusCode())
		cfg := retryCfg()
		var responded *respondedError
		if class != errClassNetwork || errors.As(err, &responded) || retries >= maxRetriesForClass(class, cfg) {
			logger.LogMessage("system", fmt.Sprintf("运行失败（%s）：%v", class, err))
			if responded != nil {
				return nil, responded.err
			}
			return nil, err
		}
		retries++
		delay := backoffDelay(class, retries, 0, cfg)
		log.Printf("%s run retry %d (%s) in %s: %v", label, retries, class, delay.Round(time.Millisecond), err)
		retrySleep(ctx, delay)
	}
}

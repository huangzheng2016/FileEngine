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

// Retries happen per model request (ModelRetryConfig), so a 429 at turn 6
// never replays turns 1-5; the run-level loop only covers network failures
// before the model answered at all.

// MaxRetries is the largest class budget so ADK never cuts a class short;
// shouldRetryModelCall enforces each class's own budget.
func modelRetryConfig() *adk.ModelRetryConfig {
	cfg := retryCfg()
	return &adk.ModelRetryConfig{
		MaxRetries:  max(maxRetriesForClass(errClassRateLimit, cfg), maxRetriesForClass(errClassServer, cfg)),
		ShouldRetry: shouldRetryModelCall,
	}
}

// The request's transport hints are consumed here so they never leak into
// the classification of a later request.
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

type runAttemptFunc func(ctx context.Context) (*schema.Message, error)

// Only network failures before any model response replay the whole run;
// everything else was already retried per request inside it.
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

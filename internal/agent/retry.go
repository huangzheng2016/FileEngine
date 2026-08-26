package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
)

// errorClass buckets a failed agent run by its retry semantics. The class is
// carried in every retry log line so cron log patrols can tell throttling
// apart from real failures at a glance.
type errorClass int

const (
	// errClassHard errors can never succeed on retry with the same input:
	// 4xx client errors (400/401/403/404/413/context-length/…) and caller
	// cancellation. They fail immediately, saving the old 3 pointless retries.
	errClassHard errorClass = iota
	// errClassRateLimit is HTTP 429. It gets its own (larger) retry budget,
	// honors server Retry-After hints, and deepens backoff fleet-wide while
	// throttling persists.
	errClassRateLimit
	// errClassServer is HTTP 5xx — plus any unrecognized error, so oddball
	// transient failures keep the old always-retry behavior.
	errClassServer
	// errClassNetwork is a transport-level failure: timeouts (including the
	// per-run agent_timeout), refused/dropped connections, DNS errors.
	errClassNetwork
)

func (c errorClass) String() string {
	switch c {
	case errClassRateLimit:
		return "rate_limit"
	case errClassServer:
		return "server"
	case errClassNetwork:
		return "network"
	default:
		return "hard"
	}
}

// errStatusCodeRe recovers an HTTP status from the rendered error text of
// go-openai error types. eino-ext converts *go-openai.APIError into its own
// *openai.APIError (caught structurally by errors.As above), but
// *go-openai.RequestError — a non-JSON error body, the usual shape of proxy
// 502/503 pages — passes through unconverted; both render as
// "error, status code: NNN, ...".
var errStatusCodeRe = regexp.MustCompile(`error, status code: (\d+)`)

// httpStatusFromError extracts the HTTP status code from a model call error,
// if the error carries one.
func httpStatusFromError(err error) (int, bool) {
	var apiErr *einoopenai.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatusCode > 0 {
		return apiErr.HTTPStatusCode, true
	}
	if m := errStatusCodeRe.FindStringSubmatch(err.Error()); m != nil {
		if code, convErr := strconv.Atoi(m[1]); convErr == nil {
			return code, true
		}
	}
	return 0, false
}

// classifyRunError decides the retry class of a failed run. The caller checks
// the parent ctx first, so a context error here means the per-attempt
// timeout, not user stop.
func classifyRunError(err error) errorClass {
	if err == nil || errors.Is(err, context.Canceled) {
		return errClassHard
	}
	if status, ok := httpStatusFromError(err); ok {
		switch {
		case status == http.StatusTooManyRequests:
			return errClassRateLimit
		case status >= 500 && status < 600:
			return errClassServer
		default:
			return errClassHard
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return errClassNetwork
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errClassNetwork
	}
	// Unknown errors stay retryable — same behavior as the old blind retry.
	return errClassServer
}

// retryAfterCarrier is implemented by error types that surface a
// server-provided Retry-After hint (e.g. a header-capturing HTTP transport
// once the model construction layer exposes one).
type retryAfterCarrier interface {
	RetryAfter() time.Duration
}

// retryAfterPatterns are a best-effort fallback for providers that put a
// retry hint only in the error message text ("please retry after 3 seconds",
// "try again in 500ms"). A bare number means seconds, per the HTTP
// Retry-After convention.
var retryAfterPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)retry[- ]after[:\s]+(\d+(?:\.\d+)?)\s*(ms|s|sec|secs|second|seconds|m|min|mins|minute|minutes)?\b`),
	regexp.MustCompile(`(?i)try again in\s+(\d+(?:\.\d+)?)\s*(ms|s|sec|secs|second|seconds|m|min|mins|minute|minutes)\b`),
}

func parseRetryAfterMessage(msg string) time.Duration {
	for _, re := range retryAfterPatterns {
		m := re.FindStringSubmatch(msg)
		if m == nil {
			continue
		}
		v, convErr := strconv.ParseFloat(m[1], 64)
		if convErr != nil || v <= 0 {
			continue
		}
		switch strings.ToLower(m[2]) {
		case "ms":
			return time.Duration(v * float64(time.Millisecond))
		case "m", "min", "mins", "minute", "minutes":
			return time.Duration(v * float64(time.Minute))
		default:
			return time.Duration(v * float64(time.Second))
		}
	}
	return 0
}

// retryAfterFromError returns the server-provided retry hint, or 0 when the
// error carries none.
func retryAfterFromError(err error) time.Duration {
	var carrier retryAfterCarrier
	if errors.As(err, &carrier) {
		if d := carrier.RetryAfter(); d > 0 {
			return d
		}
	}
	var apiErr *einoopenai.APIError
	if errors.As(err, &apiErr) {
		return parseRetryAfterMessage(apiErr.Message)
	}
	return 0
}

// retryRandFloat64 is the jitter source (stubbed in tests).
var retryRandFloat64 = rand.Float64

// rateLimitStreak counts consecutive rate_limit failures fleet-wide with no
// intervening success. While the server keeps throttling, every worker's
// backoff deepens (up to rateLimitStreakBoostMax extra exponent slots) — the
// simplest form of adaptive slowdown: no shared state beyond one atomic.
var rateLimitStreak atomic.Int64

const rateLimitStreakBoostMax = 4

// retryCfg returns the live agent config (hot-reloadable), never nil.
func retryCfg() *config.AgentConfig {
	cfg := config.Get()
	if cfg == nil {
		return &config.AgentConfig{}
	}
	return &cfg.Agent
}

// maxRetriesForClass returns the retry budget for a class: rate limiting gets
// its own larger budget, server/network errors use max_retries, hard errors
// get zero.
func maxRetriesForClass(class errorClass, cfg *config.AgentConfig) int {
	switch class {
	case errClassRateLimit:
		if cfg.RateLimitMaxRetries > 0 {
			return cfg.RateLimitMaxRetries
		}
		return 8
	case errClassServer, errClassNetwork:
		if cfg.MaxRetries > 0 {
			return cfg.MaxRetries
		}
		return 3
	default:
		return 0
	}
}

// backoffDelay computes how long to wait before retry attempt `attempt`
// (1-based): exponential backoff with full jitter,
// rand[0, min(maxDelay, base*2^(attempt-1))]. Rate limiting adds the
// fleet-wide streak to the exponent and, when the server provided a
// Retry-After hint, waits that long instead (clamped to 4*maxDelay) plus one
// jittered base slot to de-synchronize workers.
func backoffDelay(class errorClass, attempt int, retryAfter time.Duration, cfg *config.AgentConfig) time.Duration {
	base := time.Duration(cfg.RetryBaseDelay) * time.Second
	if base <= 0 {
		base = 2 * time.Second
	}
	maxDelay := time.Duration(cfg.RetryMaxDelay) * time.Second
	if maxDelay < base {
		maxDelay = base
	}

	if class == errClassRateLimit && retryAfter > 0 {
		if retryAfter > 4*maxDelay {
			retryAfter = 4 * maxDelay
		}
		return retryAfter + time.Duration(retryRandFloat64()*float64(base))
	}

	exp := attempt - 1
	if class == errClassRateLimit {
		exp += min(max(int(rateLimitStreak.Load())-1, 0), rateLimitStreakBoostMax)
	}
	if exp < 0 {
		exp = 0
	}
	raw := base
	for i := 0; i < exp; i++ {
		raw *= 2
		if raw >= maxDelay {
			raw = maxDelay
			break
		}
	}
	return time.Duration(retryRandFloat64() * float64(raw))
}

// retrySleep waits for d or returns early when ctx is done (stubbed in
// tests).
var retrySleep = func(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// runAttemptFunc executes one full agent run under ctx.
type runAttemptFunc func(ctx context.Context) (*schema.Message, error)

// runWithRetry runs attempt once, then retries failures according to their
// class: hard errors fail immediately; rate_limit/server/network errors retry
// with jittered exponential backoff until the class budget is exhausted. The
// class is re-evaluated after every attempt, so e.g. throttling turning into
// a 500 switches budget mid-loop. On total failure the last attempt's token
// usage is attributed to the system row, same as before.
func runWithRetry(ctx context.Context, attempt runAttemptFunc, logger *Logger, label string, tracker *tokenTrackingModel) (*schema.Message, error) {
	result, err := attempt(ctx)
	if err == nil {
		rateLimitStreak.Store(0)
		return result, nil
	}

	retriesUsed := 0
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		class := classifyRunError(err)
		if class == errClassRateLimit {
			rateLimitStreak.Add(1)
		}
		cfg := retryCfg()
		maxRetries := maxRetriesForClass(class, cfg)
		if retriesUsed >= maxRetries {
			if retriesUsed == 0 {
				log.Printf("%s hard error, failing without retry: %v", label, err)
			}
			break
		}
		retriesUsed++
		delay := backoffDelay(class, retriesUsed, retryAfterFromError(err), cfg)
		log.Printf("%s retry %d/%d (%s) in %s: %v", label, retriesUsed, maxRetries, class, delay.Round(time.Millisecond), err)
		retrySleep(ctx, delay)
		result, err = attempt(ctx)
		if err == nil {
			rateLimitStreak.Store(0)
			return result, nil
		}
	}

	// Attribute the last attempt's token usage to the system row too — a failed
	// batch otherwise vanishes from the token accounting (batch cards showed
	// no tokens for failed batches).
	pt, ct, tt := tracker.Usage()
	logger.LogMessage("system", fmt.Sprintf("Error after %d retries (%s): %v", retriesUsed, classifyRunError(err), err), pt, ct, tt)
	return nil, err
}

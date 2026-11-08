package agent

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"

	"FileEngine/internal/config"
)

// The class is in every retry log line so throttling is distinguishable from
// real failures.
type errorClass int

const (
	// Hard errors cannot succeed on retry with the same input: 4xx and caller
	// cancellation.
	errClassHard errorClass = iota
	errClassRateLimit
	// Unrecognized errors count as server errors so odd transient failures stay
	// retryable.
	errClassServer
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

// eino-ext converts go-openai's APIError (caught by errors.As), but a
// RequestError (non-JSON body, typical of proxy 502/503 pages) passes through
// unconverted; both render as "error, status code: NNN, ...".
var errStatusCodeRe = regexp.MustCompile(`error, status code: (\d+)`)

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

func classifyRunError(err error) errorClass {
	return classifyRunErrorWithStatus(err, 0)
}

// statusHint is the least precise source and only used when err carries no
// status. The caller checks the parent ctx first, so a context error here
// means the per-attempt timeout, not a user stop.
func classifyRunErrorWithStatus(err error, statusHint int) errorClass {
	if err == nil || errors.Is(err, context.Canceled) {
		return errClassHard
	}
	if status, ok := httpStatusFromError(err); ok {
		return classForStatus(status)
	}
	if statusHint > 0 {
		return classForStatus(statusHint)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return errClassNetwork
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errClassNetwork
	}
	return errClassServer
}

func classForStatus(status int) errorClass {
	switch {
	case status == http.StatusTooManyRequests:
		return errClassRateLimit
	case status >= 500 && status < 600:
		return errClassServer
	default:
		return errClassHard
	}
}

// Fallback for providers that put the hint only in the error payload; the
// primary source is the response header captured per attempt.
type retryAfterCarrier interface {
	RetryAfter() time.Duration
}

// A bare number means seconds, per the HTTP Retry-After convention.
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

var retryRandFloat64 = rand.Float64

// While the server keeps throttling, every worker's backoff deepens (up to
// rateLimitStreakBoostMax extra exponent slots).
var rateLimitStreak atomic.Int64

const rateLimitStreakBoostMax = 4

func retryCfg() *config.AgentConfig {
	cfg := config.Get()
	if cfg == nil {
		return &config.AgentConfig{}
	}
	return &cfg.Agent
}

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

// Full jitter: rand[0, min(maxDelay, base*2^(attempt-1))]. A Retry-After hint
// replaces it (clamped to 4*maxDelay) plus one jittered base slot to
// de-synchronize workers.
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

var retrySleep = func(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

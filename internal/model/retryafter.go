package model

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ResponseHints captures the status code and Retry-After header of the last
// HTTP error response under one request context: the eino -> go-openai chain
// drops response headers from its error types. Each attempt has its own store
// in its context, so workers can share one http.Client safely.
type ResponseHints struct {
	mu         sync.Mutex
	statusCode int
	retryAfter time.Duration
}

type responseHintsKey struct{}

func WithResponseHints(ctx context.Context) (context.Context, *ResponseHints) {
	h := &ResponseHints{}
	return context.WithValue(ctx, responseHintsKey{}, h), h
}

func ResponseHintsFromContext(ctx context.Context) *ResponseHints {
	h, _ := ctx.Value(responseHintsKey{}).(*ResponseHints)
	return h
}

func (h *ResponseHints) Record(statusCode int, retryAfter time.Duration) {
	h.mu.Lock()
	h.statusCode = statusCode
	h.retryAfter = retryAfter
	h.mu.Unlock()
}

func (h *ResponseHints) StatusCode() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.statusCode
}

func (h *ResponseHints) RetryAfter() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.retryAfter
}

// StatusTransportFailure stands in for the status of a request that never got
// a response (connection refused, reset, DNS), so classifiers can tell it
// from an HTTP error.
const StatusTransportFailure = 599

type hintCaptureTransport struct {
	base http.RoundTripper
	// noSDKRetries makes the Anthropic SDK treat every outcome as final: it
	// retries a response unless x-should-retry says otherwise, and retries a
	// missing response unconditionally, so connection errors are turned into
	// a synthetic final response.
	noSDKRetries   bool
	injectCacheKey bool
}

func (t *hintCaptureTransport) withoutSDKRetries() *hintCaptureTransport {
	t.noSDKRetries = true
	return t
}

func newHintCaptureTransport(base http.RoundTripper) *hintCaptureTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &hintCaptureTransport{base: base}
}

func (t *hintCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.injectCacheKey {
		var err error
		if req, err = withPromptCacheKey(req); err != nil {
			return nil, err
		}
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil && t.noSDKRetries && req.Context().Err() == nil {
		resp, err = transportFailureResponse(req, err), nil
	}
	if err != nil || resp == nil {
		return resp, err
	}
	if t.noSDKRetries {
		resp.Header.Set("x-should-retry", "false")
	}
	if resp.StatusCode >= http.StatusBadRequest {
		if h := ResponseHintsFromContext(req.Context()); h != nil {
			h.Record(resp.StatusCode, parseRetryAfterHeader(resp.Header.Get("Retry-After")))
		}
	}
	return resp, nil
}

func transportFailureResponse(req *http.Request, cause error) *http.Response {
	body, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{
		"type": "api_error", "message": "request failed before a response arrived: " + cause.Error(),
	}})
	return &http.Response{
		Status:        http.StatusText(StatusTransportFailure),
		StatusCode:    StatusTransportFailure,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// ParseRetryAfter reads a Retry-After header value (seconds or HTTP date).
func ParseRetryAfter(v string) time.Duration { return parseRetryAfterHeader(v) }

func parseRetryAfterHeader(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n <= 0 {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	if ts, err := http.ParseTime(v); err == nil {
		if d := time.Until(ts); d > 0 {
			return d
		}
	}
	return 0
}

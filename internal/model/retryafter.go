package model

import (
	"context"
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

type hintCaptureTransport struct {
	base http.RoundTripper
}

func newHintCaptureTransport(base http.RoundTripper) *hintCaptureTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &hintCaptureTransport{base: base}
}

func (t *hintCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		if h := ResponseHintsFromContext(req.Context()); h != nil {
			h.Record(resp.StatusCode, parseRetryAfterHeader(resp.Header.Get("Retry-After")))
		}
	}
	return resp, nil
}

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

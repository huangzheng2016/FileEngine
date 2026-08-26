package model

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ResponseHints captures transport-level hints from the most recent HTTP
// error response (status >= 400) seen under one request context: its status
// code and Retry-After header. The eino → go-openai chain drops response
// headers from its error types, so the agent retry classifier cannot see
// them; attaching a hints store to the per-attempt context lets the injected
// hintCaptureTransport pass them up without forking the chain.
//
// One store is shared by every HTTP request made under the same attempt
// context and overwritten by each error response (last wins — the last failed
// request is the one that ends the run). Stores of concurrent attempts never
// mix: each request reads its own store from its own context, so the 10
// workers can share one http.Client safely.
type ResponseHints struct {
	mu         sync.Mutex
	statusCode int
	retryAfter time.Duration
}

type responseHintsKey struct{}

// WithResponseHints returns a child context carrying a fresh ResponseHints
// store plus the store itself.
func WithResponseHints(ctx context.Context) (context.Context, *ResponseHints) {
	h := &ResponseHints{}
	return context.WithValue(ctx, responseHintsKey{}, h), h
}

// ResponseHintsFromContext returns the store attached by WithResponseHints,
// or nil.
func ResponseHintsFromContext(ctx context.Context) *ResponseHints {
	h, _ := ctx.Value(responseHintsKey{}).(*ResponseHints)
	return h
}

// Record overwrites the hints with an error response's status and Retry-After
// (0 = none). Exported so any transport layer can feed the store.
func (h *ResponseHints) Record(statusCode int, retryAfter time.Duration) {
	h.mu.Lock()
	h.statusCode = statusCode
	h.retryAfter = retryAfter
	h.mu.Unlock()
}

// StatusCode returns the last recorded error response's status, 0 if none.
func (h *ResponseHints) StatusCode() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.statusCode
}

// RetryAfter returns the last recorded error response's Retry-After, 0 if
// none.
func (h *ResponseHints) RetryAfter() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.retryAfter
}

// hintCaptureTransport records status code + Retry-After of error responses
// into the request context's ResponseHints store, then passes the response
// through untouched.
type hintCaptureTransport struct {
	base http.RoundTripper
}

// newHintCaptureTransport wraps base (nil = http.DefaultTransport).
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

// parseRetryAfterHeader parses both Retry-After forms: delay-seconds and
// HTTP-date. Returns 0 for absent/invalid/past values (treated as no hint).
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

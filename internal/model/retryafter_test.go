package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/db"
)

func TestParseRetryAfterHeader(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"delay seconds", "120", 120 * time.Second},
		{"whitespace padded", "  7 ", 7 * time.Second},
		{"zero means no hint", "0", 0},
		{"negative means no hint", "-5", 0},
		{"empty", "", 0},
		{"garbage", "soon-ish", 0},
		{"past http date", time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfterHeader(tc.in); got != tc.want {
				t.Fatalf("parseRetryAfterHeader(%q): got %v, want %v", tc.in, got, tc.want)
			}
		})
	}

	// HTTP-date form: a timestamp ~30s out parses to roughly 30s.
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfterHeader(future); got < 25*time.Second || got > 31*time.Second {
		t.Fatalf("http date form: got %v, want ~30s", got)
	}
}

type fakeRoundTripper struct {
	status int
	header http.Header
	body   string
	err    error
}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.status,
		Status:     fmt.Sprintf("%d fake", f.status),
		Header:     f.header,
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Request:    req,
	}, nil
}

func TestHintCaptureTransportRecordsErrorResponses(t *testing.T) {
	cases := []struct {
		name             string
		status           int
		retryAfterHeader string
		wantStatus       int
		wantRetryAfter   time.Duration
	}{
		{"429 with retry-after", 429, "7", 429, 7 * time.Second},
		{"429 with http date", 429, "", 429, 0}, // header set below
		{"503 without header", 503, "", 503, 0},
		{"400", 400, "3", 400, 3 * time.Second},
		{"200 success not recorded", 200, "9", 0, 0},
		{"302 redirect not recorded", 302, "9", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			if tc.retryAfterHeader != "" {
				header.Set("Retry-After", tc.retryAfterHeader)
			}
			if tc.name == "429 with http date" {
				header.Set("Retry-After", time.Now().Add(45*time.Second).UTC().Format(http.TimeFormat))
			}
			rt := newHintCaptureTransport(&fakeRoundTripper{status: tc.status, header: header})
			ctx, hints := WithResponseHints(context.Background())
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://x/v1/chat/completions", nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("response passthrough: got status %d, want %d", resp.StatusCode, tc.status)
			}
			if tc.name == "429 with http date" {
				if got := hints.RetryAfter(); got < 40*time.Second || got > 46*time.Second {
					t.Fatalf("http date hint: got %v, want ~45s", got)
				}
				return
			}
			if got := hints.StatusCode(); got != tc.wantStatus {
				t.Fatalf("status: got %d, want %d", got, tc.wantStatus)
			}
			if got := hints.RetryAfter(); got != tc.wantRetryAfter {
				t.Fatalf("retry-after: got %v, want %v", got, tc.wantRetryAfter)
			}
		})
	}
}

func TestHintCaptureTransportWithoutHintsContext(t *testing.T) {
	rt := newHintCaptureTransport(&fakeRoundTripper{status: 429, header: http.Header{"Retry-After": {"5"}}})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://x/v1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := rt.RoundTrip(req) // must not panic without a hints store
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if ResponseHintsFromContext(req.Context()) != nil {
		t.Fatal("no store must be attached for a bare context")
	}
}

func TestHintCaptureTransportPropagatesBaseError(t *testing.T) {
	baseErr := errors.New("connection refused")
	rt := newHintCaptureTransport(&fakeRoundTripper{err: baseErr})
	ctx, hints := WithResponseHints(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://x/v1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if _, err := rt.RoundTrip(req); !errors.Is(err, baseErr) {
		t.Fatalf("got err=%v, want %v", err, baseErr)
	}
	if hints.StatusCode() != 0 || hints.RetryAfter() != 0 {
		t.Fatalf("transport errors record nothing, got %d/%v", hints.StatusCode(), hints.RetryAfter())
	}
}

// Concurrent requests through one shared transport must each record into
// their own context's store — never into a neighbor's.
func TestHintCaptureTransportConcurrentIsolation(t *testing.T) {
	rt := newHintCaptureTransport(&fakeRoundTripper{status: 429, header: http.Header{"Retry-After": {"5"}}})
	const workers = 20
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, hints := WithResponseHints(context.Background())
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://x/v1", nil)
			if err != nil {
				t.Errorf("new request: %v", err)
				return
			}
			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Errorf("RoundTrip: %v", err)
				return
			}
			defer resp.Body.Close()
			if got := hints.StatusCode(); got != 429 {
				t.Errorf("status: got %d, want 429", got)
			}
			if got := hints.RetryAfter(); got != 5*time.Second {
				t.Errorf("retry-after: got %v, want 5s", got)
			}
		}()
	}
	wg.Wait()
}

// End-to-end through the real eino openai chain: an httptest server answers
// 429 + Retry-After, and the hints attached to the Generate context must
// surface both — this is the wiring the agent retry loop relies on.
func TestOpenAIModelCapturesRetryAfterEndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down","type":"rate_limit_error"}}`))
	}))
	defer server.Close()

	chatModel, err := NewChatModelFromProvider(context.Background(), &db.ModelProvider{Provider: "openai", APIKey: "fake-key", Model: "fake-model", BaseURL: server.URL, Temperature: 0.7})
	if err != nil {
		t.Fatalf("NewChatModelFromProvider: %v", err)
	}
	ctx, hints := WithResponseHints(context.Background())
	_, err = chatModel.Generate(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err == nil {
		t.Fatal("want 429 error from Generate")
	}
	if got := hints.StatusCode(); got != 429 {
		t.Fatalf("hints status: got %d, want 429", got)
	}
	if got := hints.RetryAfter(); got != 7*time.Second {
		t.Fatalf("hints retry-after: got %v, want 7s", got)
	}
}

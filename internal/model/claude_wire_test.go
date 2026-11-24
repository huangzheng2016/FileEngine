package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"FileEngine/internal/db"

	"github.com/anthropics/anthropic-sdk-go"
	claude "github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino/schema"
)

// fakeAnthropic records request bodies and answers each with handler(n).
type fakeAnthropic struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newFakeAnthropic(t *testing.T, handler func(n int, w http.ResponseWriter)) *fakeAnthropic {
	f := &fakeAnthropic{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(b))
		n := len(f.bodies)
		f.mu.Unlock()
		handler(n, w)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAnthropic) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies...)
}

func sseText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"usage":{"input_tokens":10,"cache_read_input_tokens":4,"cache_creation_input_tokens":2,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text[:len(text)/2]),
		fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text[len(text)/2:]),
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
		`{"type":"message_stop"}`,
	}
	for _, e := range events {
		var head struct{ Type string }
		_ = json.Unmarshal([]byte(e), &head)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", head.Type, e)
	}
}

func apiError(w http.ResponseWriter, status int, typ string, header map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	for k, v := range header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":"x"}}`, typ)
}

func newTestClaude(t *testing.T, f *fakeAnthropic, p db.ModelProvider) *claudeModel {
	p.Provider, p.APIKey, p.BaseURL = "claude", "k", f.URL
	if p.Model == "" {
		p.Model = "claude-opus-5-5"
	}
	m, err := NewChatModelFromProvider(context.Background(), &p)
	if err != nil {
		t.Fatal(err)
	}
	return m.(*claudeModel)
}

func toolRound() (asst *schema.Message, tool *schema.Message) {
	asst = &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "tc1", Function: schema.FunctionCall{Name: "t1", Arguments: `{}`}}}}
	return asst, schema.ToolMessage("r", "tc1")
}

func TestClaudeBreakpointKeyMatchesEino(t *testing.T) {
	if !hasBreakpoint(claude.SetMessageBreakpoint(schema.UserMessage("x"))) {
		t.Fatal("claudeBreakpointKey no longer matches the eino claude package")
	}
}

func TestClaudeGenerateStreamsWithoutMaxTokens(t *testing.T) {
	f := newFakeAnthropic(t, func(_ int, w http.ResponseWriter) { sseText(w, "hello world") })
	m := newTestClaude(t, f, db.ModelProvider{})
	msg, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "hello world" {
		t.Fatalf("content = %q", msg.Content)
	}
	u := msg.ResponseMeta.Usage
	if u == nil || u.PromptTokens != 16 || u.PromptTokenDetails.CachedTokens != 4 || u.PromptTokenDetails.CacheWriteTokens != 2 || u.CompletionTokens != 7 {
		t.Fatalf("usage = %+v", u)
	}
	reqs := f.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	var body struct {
		Stream    bool  `json:"stream"`
		MaxTokens int64 `json:"max_tokens"`
	}
	if err := json.Unmarshal([]byte(reqs[0]), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Stream || body.MaxTokens != 64000 {
		t.Fatalf("stream=%v max_tokens=%d, want streaming with the model default", body.Stream, body.MaxTokens)
	}
}

func TestClaudeDefaultMaxTokens(t *testing.T) {
	for name, want := range map[string]int{
		"claude-opus-5-5": 64000, "claude-sonnet-4-5": 64000, "my-alias": 64000,
		"claude-opus-4-1-20250805": 32000, "claude-3-5-sonnet-latest": 8192, "claude-3-haiku-20240307": 4096,
	} {
		if got := claudeDefaultMaxTokens(name); got != want {
			t.Errorf("%s: %d, want %d", name, got, want)
		}
	}
}

// Marks the four places the API allows: last tool, system, shared context
// (explicit) and the last message.
func TestClaudeFourBreakpoints(t *testing.T) {
	f := newFakeAnthropic(t, func(_ int, w http.ResponseWriter) { sseText(w, "ok") })
	m := newTestClaude(t, f, db.ModelProvider{ThinkingEnabled: true, ReasoningEffort: "high"})
	if err := m.BindTools([]*schema.ToolInfo{{Name: "t1", Desc: "d"}, {Name: "t2", Desc: "d"}}); err != nil {
		t.Fatal(err)
	}
	asst, tool := toolRound()
	shared := MarkCacheBreakpoint(schema.UserMessage("SHARED"))
	input := []*schema.Message{schema.SystemMessage("SYS"), shared, schema.UserMessage("UNIT"), asst, tool}
	if _, err := m.Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if hasBreakpoint(tool) {
		t.Fatal("caller's message was modified")
	}

	var body struct {
		Tools    []map[string]any `json:"tools"`
		System   []map[string]any `json:"system"`
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	raw := f.requests()[0]
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(raw, `"cache_control"`); n != 4 {
		t.Fatalf("cache_control markers = %d, want 4\n%s", n, raw)
	}
	marked := func(b map[string]any) bool { _, ok := b["cache_control"]; return ok }
	if marked(body.Tools[0]) || !marked(body.Tools[1]) {
		t.Errorf("tool marks: %v %v", marked(body.Tools[0]), marked(body.Tools[1]))
	}
	if !marked(body.System[0]) {
		t.Error("system not marked")
	}
	// messages: shared+unit merge into one user message only if adjacent; check by block text.
	var markedTexts []string
	for _, msg := range body.Messages {
		for _, b := range msg.Content {
			if marked(b) {
				if s, ok := b["text"].(string); ok {
					markedTexts = append(markedTexts, s)
				} else {
					markedTexts = append(markedTexts, "<"+fmt.Sprint(b["type"])+">")
				}
			}
		}
	}
	if len(markedTexts) != 2 || markedTexts[0] != "SHARED" || markedTexts[1] != "<tool_result>" {
		t.Fatalf("marked message blocks = %v", markedTexts)
	}
}

func TestTailBreakpointNeverExceedsLimit(t *testing.T) {
	u := func(s string) *schema.Message { return schema.UserMessage(s) }
	three := []*schema.Message{MarkCacheBreakpoint(u("a")), MarkCacheBreakpoint(u("b")), u("c")}
	if got := withTailBreakpoint(three); hasBreakpoint(got[2]) {
		t.Fatal("tail marked although two explicit marks already use the budget")
	}
	one := []*schema.Message{schema.SystemMessage("s"), MarkCacheBreakpoint(u("a")), u("b")}
	if got := withTailBreakpoint(one); !hasBreakpoint(got[2]) || hasBreakpoint(one[2]) {
		t.Fatal("tail must be marked on a copy")
	}
	if got := withTailBreakpoint(nil); got != nil {
		t.Fatal("empty input")
	}
}

// Every request of a run carries the same thinking and effort, and a request
// that appends to the history keeps the earlier messages byte for byte.
func TestClaudeThinkingConstantAndPrefixStable(t *testing.T) {
	f := newFakeAnthropic(t, func(_ int, w http.ResponseWriter) { sseText(w, "ok") })
	m := newTestClaude(t, f, db.ModelProvider{ThinkingEnabled: true, ReasoningEffort: "medium"})
	m.BindTools([]*schema.ToolInfo{{Name: "t1", Desc: "d"}})
	asst, tool := toolRound()
	base := []*schema.Message{schema.SystemMessage("SYS"), MarkCacheBreakpoint(schema.UserMessage("SHARED")), schema.UserMessage("UNIT")}
	for _, in := range [][]*schema.Message{base, append(append([]*schema.Message{}, base...), asst, tool)} {
		if _, err := m.Generate(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	type req struct {
		Thinking     json.RawMessage   `json:"thinking"`
		OutputConfig json.RawMessage   `json:"output_config"`
		Messages     []json.RawMessage `json:"messages"`
	}
	var a, b req
	reqs := f.requests()
	json.Unmarshal([]byte(reqs[0]), &a)
	json.Unmarshal([]byte(reqs[1]), &b)
	if string(a.Thinking) != `{"type":"adaptive"}` || string(a.OutputConfig) != `{"effort":"medium"}` {
		t.Fatalf("thinking=%s output_config=%s", a.Thinking, a.OutputConfig)
	}
	if string(a.Thinking) != string(b.Thinking) || string(a.OutputConfig) != string(b.OutputConfig) {
		t.Fatal("thinking or effort changed between requests")
	}
	// Message 0 carries the tail mark in request 1 but not in request 2, so it is compared
	// without cache_control: the cached prefix is matched on content, not on markers.
	strip := func(s string) string { return strings.ReplaceAll(s, `,"cache_control":{"type":"ephemeral"}`, "") }
	if strip(string(a.Messages[0])) != strip(string(b.Messages[0])) {
		t.Fatalf("message 0 differs:\n%s\n%s", a.Messages[0], b.Messages[0])
	}
}

func TestClaudeEffortIndependentOfThinkingToggle(t *testing.T) {
	cfg := claudeConfig(&db.ModelProvider{Model: "claude-opus-5-5", ReasoningEffort: "low"})
	if cfg.ThinkingConfig != nil || cfg.AdditionalRequestFields["output_config"].(map[string]any)["effort"] != "low" {
		t.Fatalf("%+v", cfg)
	}
	if legacy := claudeConfig(&db.ModelProvider{Model: "claude-sonnet-4-5", ReasoningEffort: "low"}); legacy.AdditionalRequestFields != nil {
		t.Fatal("effort must not be sent to models that lack it")
	}
}

// The SDK must not retry on its own: one HTTP request per Generate, whatever
// the failure, with the status and Retry-After left for the app's classifier.
func TestClaudeSDKDoesNotRetry(t *testing.T) {
	cases := []struct {
		name    string
		handler func(n int, w http.ResponseWriter)
		status  int
		after   time.Duration
	}{
		{"429 with Retry-After", func(_ int, w http.ResponseWriter) {
			apiError(w, 429, "rate_limit_error", map[string]string{"Retry-After": "3"})
		}, 429, 3 * time.Second},
		{"429 saying retry", func(_ int, w http.ResponseWriter) {
			apiError(w, 429, "rate_limit_error", map[string]string{"x-should-retry": "true"})
		}, 429, 0},
		{"529 overloaded", func(_ int, w http.ResponseWriter) { apiError(w, 529, "overloaded_error", nil) }, 529, 0},
		{"500", func(_ int, w http.ResponseWriter) { apiError(w, 500, "api_error", nil) }, 500, 0},
		{"400", func(_ int, w http.ResponseWriter) { apiError(w, 400, "invalid_request_error", nil) }, 400, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeAnthropic(t, c.handler)
			m := newTestClaude(t, f, db.ModelProvider{})
			ctx, hints := WithResponseHints(context.Background())
			_, err := m.Generate(ctx, []*schema.Message{schema.UserMessage("hi")})
			var ae *anthropic.Error
			if !errors.As(err, &ae) || ae.StatusCode != c.status {
				t.Fatalf("err = %v", err)
			}
			if n := len(f.requests()); n != 1 {
				t.Fatalf("HTTP requests = %d, want 1", n)
			}
			if hints.StatusCode() != c.status || hints.RetryAfter() != c.after {
				t.Fatalf("hints = %d / %s", hints.StatusCode(), hints.RetryAfter())
			}
			if c.after > 0 {
				if got := ParseRetryAfter(ae.Response.Header.Get("Retry-After")); got != c.after {
					t.Fatalf("error carries Retry-After %s", got)
				}
			}
		})
	}
}

func TestClaudeConnectionErrorIsNotRetriedBySDK(t *testing.T) {
	var attempts int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer srv.Close()
	m, err := NewChatModelFromProvider(context.Background(), &db.ModelProvider{Provider: "claude", Model: "claude-opus-5-5", APIKey: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, hints := WithResponseHints(context.Background())
	_, err = m.Generate(ctx, []*schema.Message{schema.UserMessage("hi")})
	var ae *anthropic.Error
	if !errors.As(err, &ae) || ae.StatusCode != StatusTransportFailure {
		t.Fatalf("err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 || hints.StatusCode() != StatusTransportFailure {
		t.Fatalf("attempts = %d, hint status = %d", attempts, hints.StatusCode())
	}
}

func TestOpenAIPromptCacheKey(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	for _, provider := range []string{"openai", "ollama"} {
		p := &db.ModelProvider{Provider: provider, Model: "m", APIKey: "k", BaseURL: srv.URL + "/v1"}
		m, err := NewChatModelFromProvider(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		in := []*schema.Message{schema.UserMessage("hi")}
		if _, err := m.Generate(WithPromptCacheKey(context.Background(), 42, "tagging"), in); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Generate(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 4 {
		t.Fatalf("requests = %d", len(bodies))
	}
	var withKey map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &withKey); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, bodies[0])
	}
	if withKey["prompt_cache_key"] != "fe:42:tagging" || withKey["model"] != "m" {
		t.Fatalf("openai body: %s", bodies[0])
	}
	for i, why := range map[int]string{1: "openai without a key", 2: "ollama with a key", 3: "ollama without a key"} {
		if strings.Contains(bodies[i], "prompt_cache_key") {
			t.Errorf("%s must not send prompt_cache_key: %s", why, bodies[i])
		}
	}
}

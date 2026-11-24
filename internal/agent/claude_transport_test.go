package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"
)

type claudeServer struct {
	*httptest.Server
	mu    sync.Mutex
	count int
}

func newClaudeServer(t *testing.T, handler func(n int, w http.ResponseWriter)) *claudeServer {
	s := &claudeServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		s.mu.Lock()
		s.count++
		n := s.count
		s.mu.Unlock()
		handler(n, w)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *claudeServer) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

func claudeErr(w http.ResponseWriter, status int, typ string, retryAfter string) {
	w.Header().Set("Content-Type", "application/json")
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":"x"}}`, typ)
}

func claudeSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range []string{
		`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"m","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text),
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	} {
		var head struct{ Type string }
		_ = json.Unmarshal([]byte(e), &head)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", head.Type, e)
	}
}

func runAgainst(t *testing.T, s *claudeServer) (*schema.Message, error, time.Duration) {
	t.Helper()
	setupToolTestRepo(t)
	m, err := modelfactory.NewChatModelFromProvider(context.Background(),
		&db.ModelProvider{Provider: "claude", Model: "claude-opus-5-5", APIKey: "k", BaseURL: s.URL})
	if err != nil {
		t.Fatal(err)
	}
	tracker := newTokenTrackingModel(m)
	start := time.Now()
	msg, err := runADKWithRetry(context.Background(), newEchoAgent(t, tracker, nil),
		[]*schema.Message{schema.UserMessage("go")}, NewLogger(300, 1, 0), "test", tracker)
	return msg, err, time.Since(start)
}

// 429 with Retry-After: the SDK stays out of it, the app retries once after
// at least the advertised wait.
func TestClaudeRateLimitRetriedByAppHonoringRetryAfter(t *testing.T) {
	config.Update(&config.Config{Agent: config.AgentConfig{MaxRetries: 2, RateLimitMaxRetries: 3, RetryBaseDelay: 1, RetryMaxDelay: 2}})
	s := newClaudeServer(t, func(n int, w http.ResponseWriter) {
		if n == 1 {
			claudeErr(w, 429, "rate_limit_error", "1")
			return
		}
		claudeSSE(w, "done")
	})
	msg, err, took := runAgainst(t, s)
	if err != nil || msg == nil || msg.Content != "done" {
		t.Fatalf("msg=%+v err=%v", msg, err)
	}
	if got := s.requests(); got != 2 {
		t.Fatalf("HTTP requests = %d, want 2 (one rejected, one retry by the app)", got)
	}
	if took < time.Second {
		t.Fatalf("retry came after %s, before Retry-After", took)
	}
}

func TestClaudeBadRequestNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404} {
		config.Update(&config.Config{Agent: config.AgentConfig{MaxRetries: 2, RateLimitMaxRetries: 3, RetryBaseDelay: 1}})
		s := newClaudeServer(t, func(_ int, w http.ResponseWriter) { claudeErr(w, status, "invalid_request_error", "") })
		if _, err, _ := runAgainst(t, s); err == nil {
			t.Fatalf("%d: expected an error", status)
		}
		if got := s.requests(); got != 1 {
			t.Fatalf("%d: HTTP requests = %d, want 1", status, got)
		}
	}
}

func TestClaudeErrorClassification(t *testing.T) {
	for status, want := range map[int]errorClass{
		429: errClassRateLimit, 500: errClassServer, 529: errClassServer, 408: errClassServer,
		400: errClassHard, 401: errClassHard, 403: errClassHard, 404: errClassHard,
		modelfactory.StatusTransportFailure: errClassNetwork,
	} {
		s := newClaudeServer(t, func(_ int, w http.ResponseWriter) { claudeErr(w, status, "api_error", "7") })
		if status == modelfactory.StatusTransportFailure {
			s.Close()
		}
		m, _ := modelfactory.NewChatModelFromProvider(context.Background(),
			&db.ModelProvider{Provider: "claude", Model: "claude-opus-5-5", APIKey: "k", BaseURL: s.URL})
		_, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
		if got := classifyRunErrorWithStatus(err, 0); got != want {
			t.Errorf("status %d: class %s, want %s (%v)", status, got, want, err)
		}
		if status == 429 {
			if d := retryAfterFromError(err); d != 7*time.Second {
				t.Errorf("Retry-After from error = %s", d)
			}
		}
	}
}

func clearTestAgentTool(t *testing.T) tool.BaseTool {
	type in struct {
		Text string `json:"text"`
	}
	tl, err := utils.InferTool("echo", "echo", func(_ context.Context, i *in) (*in, error) { return i, nil })
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

func toolRounds(n int, size int, withImage bool) []*schema.Message {
	msgs := []*schema.Message{schema.UserMessage("go")}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("c%d", i)
		msgs = append(msgs, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{ID: id, Function: schema.FunctionCall{Name: "echo", Arguments: `{"text":"` + strings.Repeat("a", 5) + `"}`}}}})
		res := schema.ToolMessage(strings.Repeat("字", size), id, schema.WithToolName("echo"))
		if withImage && i == 0 {
			res = &schema.Message{Role: schema.Tool, ToolCallID: id, ToolName: "echo", UserInputMultiContent: []schema.MessageInputPart{
				{Type: schema.ChatMessagePartTypeText, Text: "img"},
				{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{Base64Data: strPtr("AAAA"), MIMEType: "image/png"}}},
			}}
		}
		msgs = append(msgs, res)
	}
	return msgs
}

func strPtr(s string) *string { return &s }

func rewrite(t *testing.T, p reductionProfile, msgs []*schema.Message) []*schema.Message {
	t.Helper()
	mw, err := buildReduction(context.Background(), []tool.BaseTool{clearTestAgentTool(t)}, p, 200_000)
	if err != nil {
		t.Fatal(err)
	}
	_, st, err := mw.BeforeModelRewriteState(context.Background(), &adk.ChatModelAgentState{Messages: msgs}, &adk.ModelContext{})
	if err != nil {
		t.Fatal(err)
	}
	return st.Messages
}

func isCleared(m *schema.Message) bool {
	return strings.Contains(m.Content, "已清除") || (len(m.UserInputMultiContent) == 1 && strings.Contains(m.UserInputMultiContent[0].Text, "已清除"))
}

// Clearing is one batch: nothing below the threshold, nothing when a clear
// would free too little, and otherwise everything but the last three rounds,
// images included.
func TestToolResultClearingIsBatched(t *testing.T) {
	p := reductionProfile{
		clearAt:      func(int) int64 { return 3000 },
		clearAtLeast: func(int64) int64 { return 2000 },
		keepRounds:   3,
		truncChars:   1 << 20,
	}

	small := toolRounds(8, 10, false)
	for i, m := range rewrite(t, p, small) {
		if isCleared(m) {
			t.Fatalf("below threshold: message %d cleared", i)
		}
	}

	big := toolRounds(8, 1200, true) // about 800 tokens per result
	out := rewrite(t, p, big)
	var cleared []int
	for i, m := range out {
		if m.Role == schema.Tool && isCleared(m) {
			cleared = append(cleared, i)
		}
	}
	if len(cleared) != 5 {
		t.Fatalf("cleared tool results at %v, want the 5 oldest of 8", cleared)
	}
	for _, i := range []int{len(out) - 1, len(out) - 3, len(out) - 5} {
		if isCleared(out[i]) {
			t.Fatalf("one of the last 3 results (message %d) was cleared", i)
		}
	}
	if len(out[2].UserInputMultiContent) != 1 || out[2].UserInputMultiContent[0].Type != schema.ChatMessagePartTypeText {
		t.Fatalf("image result not replaced by text: %+v", out[2].UserInputMultiContent)
	}

	// Over the threshold, but the clearable part is too small to be worth a cache break.
	p.clearAtLeast = func(int64) int64 { return 1_000_000 }
	for i, m := range rewrite(t, p, toolRounds(8, 1200, false)) {
		if isCleared(m) {
			t.Fatalf("clear below the minimum batch: message %d cleared", i)
		}
	}
}

func TestClearToolResultDropsLongArguments(t *testing.T) {
	long := &schema.ToolArgument{Text: `{"items":"` + strings.Repeat("x", 2000) + `"}`}
	r, err := clearToolResult(context.Background(), &reduction.ToolDetail{ToolArgument: long, ToolResult: &schema.ToolResult{}})
	if err != nil || !r.NeedClear || !json.Valid([]byte(r.ToolArgument.Text)) || len(r.ToolArgument.Text) > 200 {
		t.Fatalf("%+v %v", r, err)
	}
	short := &schema.ToolArgument{Text: `{"path":"a"}`}
	if r, _ := clearToolResult(context.Background(), &reduction.ToolDetail{ToolArgument: short, ToolResult: &schema.ToolResult{}}); r.ToolArgument.Text != short.Text {
		t.Fatal("short arguments must stay")
	}
}

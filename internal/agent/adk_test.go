package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

// scriptedModel streams a reasoning + text + tool-call answer on its first
// call and a plain text answer on its second. emptyCallID simulates models
// that omit tool call ids.
type scriptedModel struct {
	calls       int
	emptyCallID bool
}

func (m *scriptedModel) chunks() []*schema.Message {
	m.calls++
	usage := &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}
	if m.calls == 1 {
		id := "call_1"
		if m.emptyCallID {
			id = ""
		}
		idx := 0
		return []*schema.Message{
			{Role: schema.Assistant, ReasoningContent: "think"},
			{Role: schema.Assistant, Content: "Let me "},
			{Role: schema.Assistant, Content: "echo."},
			{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &idx, ID: id, Type: "function",
				Function: schema.FunctionCall{Name: "echo", Arguments: `{"text":`}}}},
			{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &idx,
				Function: schema.FunctionCall{Arguments: `"hi"}`}}}, ResponseMeta: usage},
		}
	}
	return []*schema.Message{
		{Role: schema.Assistant, Content: "do"},
		{Role: schema.Assistant, Content: "ne", ResponseMeta: usage},
	}
}

func (m *scriptedModel) Generate(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	return schema.ConcatMessages(m.chunks())
}

func (m *scriptedModel) Stream(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray(m.chunks()), nil
}

func (m *scriptedModel) BindTools(_ []*schema.ToolInfo) error { return nil }

type echoInput struct {
	Text string `json:"text" jsonschema_description:"text to echo"`
}

type echoOutput struct {
	Text string `json:"text"`
}

func newEchoAgent(t *testing.T, model einomodel.ChatModel, echo tool.BaseTool) *adk.ChatModelAgent {
	t.Helper()
	if config.Get() == nil {
		config.Update(&config.Config{})
	}
	if echo == nil {
		var err error
		echo, err = utils.InferTool("echo", "echo back the text", func(_ context.Context, in *echoInput) (*echoOutput, error) {
			return &echoOutput{Text: in.Text}, nil
		})
		if err != nil {
			t.Fatalf("build echo tool: %v", err)
		}
	}
	a, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name:             "test-agent",
		Model:            model,
		ToolsConfig:      adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{echo}}},
		MaxIterations:    3,
		ModelRetryConfig: modelRetryConfig(),
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return a
}

type streamEvent struct {
	Type string
	Data map[string]any
}

// collectEvents flushes pending deltas and drains everything sub received.
func collectEvents(t *testing.T, sub *db.Subscriber) []streamEvent {
	t.Helper()
	db.FlushEvents()
	var out []streamEvent
	for {
		select {
		case ev := <-sub.C:
			var d map[string]any
			if err := json.Unmarshal([]byte(ev.Data), &d); err != nil {
				t.Fatalf("event %s data: %v", ev.Type, err)
			}
			out = append(out, streamEvent{Type: ev.Type, Data: d})
		case <-time.After(100 * time.Millisecond):
			return out
		}
	}
}

func TestRunADKOnceStreamsEvents(t *testing.T) {
	setupToolTestRepo(t)
	const sid = 200
	sub := db.Subscribe(sid)
	defer db.Unsubscribe(sub)

	logger := NewLogger(sid, 9, 5)
	msg, err := runADKOnce(context.Background(), newEchoAgent(t, &scriptedModel{}, nil), []*schema.Message{{Role: schema.User, Content: "go"}}, logger)
	if err != nil {
		t.Fatalf("runADKOnce: %v", err)
	}
	if msg == nil || msg.Content != "done" {
		t.Fatalf("final message: %+v", msg)
	}

	evs := collectEvents(t, sub)
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
		if e.Data["run_id"] != float64(9) || e.Data["worker_id"] != float64(5) {
			t.Fatalf("%s lacks run/worker ids: %v", e.Type, e.Data)
		}
	}
	want := []string{db.EvReasoning, db.EvText, db.EvMessage, db.EvToolCall, db.EvToolResult, db.EvText, db.EvMessage}
	if len(types) != len(want) {
		t.Fatalf("event types = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("event types = %v, want %v", types, want)
		}
	}

	m1 := evs[0].Data["msg_id"]
	if evs[1].Data["msg_id"] != m1 || evs[2].Data["msg_id"] != m1 {
		t.Fatalf("first message deltas and finalization must share msg_id: %v", evs[:3])
	}
	if evs[0].Data["delta"] != "think" || evs[1].Data["delta"] != "Let me echo." {
		t.Fatalf("coalesced deltas: %v / %v", evs[0].Data, evs[1].Data)
	}
	if evs[2].Data["role"] != "assistant" || evs[2].Data["content"] != "Let me echo." {
		t.Fatalf("finalizing message: %v", evs[2].Data)
	}

	call, res := evs[3].Data, evs[4].Data
	args, _ := call["args"].(map[string]any)
	if call["call_id"] != "call_1" || call["name"] != "echo" || args["text"] != "hi" {
		t.Fatalf("tool_call: %v", call)
	}
	out, _ := res["output"].(map[string]any)
	if res["call_id"] != "call_1" || res["name"] != "echo" || res["ok"] != true || out["text"] != "hi" {
		t.Fatalf("tool_result: %v", res)
	}
	if _, has := res["duration_ms"]; !has {
		t.Fatalf("tool_result lacks duration_ms: %v", res)
	}

	m2 := evs[5].Data["msg_id"]
	if m2 == m1 || evs[6].Data["msg_id"] != m2 || evs[6].Data["content"] != "done" {
		t.Fatalf("second message: %v / %v", evs[5].Data, evs[6].Data)
	}
}

// Models that omit tool call ids still get a generated call_id that pairs
// the tool_call with its tool_result.
func TestRunADKOnceGeneratesCallIDs(t *testing.T) {
	setupToolTestRepo(t)
	const sid = 202
	sub := db.Subscribe(sid)
	defer db.Unsubscribe(sub)

	logger := NewLogger(sid, 1, 0)
	if _, err := runADKOnce(context.Background(), newEchoAgent(t, &scriptedModel{emptyCallID: true}, nil), []*schema.Message{{Role: schema.User, Content: "go"}}, logger); err != nil {
		t.Fatalf("runADKOnce: %v", err)
	}
	var callID, resultID any
	for _, e := range collectEvents(t, sub) {
		switch e.Type {
		case db.EvToolCall:
			callID = e.Data["call_id"]
		case db.EvToolResult:
			resultID = e.Data["call_id"]
		}
	}
	if callID == nil || callID == "" || callID != resultID {
		t.Fatalf("call_id %v / result call_id %v", callID, resultID)
	}
}

// Under streaming the token tracker still counts usage (Stream path), and
// runADKWithRetry adds it to the run totals.
func TestRunADKWithRetryCountsStreamingTokens(t *testing.T) {
	setupToolTestRepo(t)
	tracker := newTokenTrackingModel(&scriptedModel{})
	logger := NewLogger(203, 1, 0)
	if _, err := runADKWithRetry(context.Background(), newEchoAgent(t, tracker, nil), []*schema.Message{{Role: schema.User, Content: "go"}}, logger, "test", tracker); err != nil {
		t.Fatalf("runADKWithRetry: %v", err)
	}
	tok := logger.Tokens()
	if tok.Prompt != 20 || tok.Completion != 10 || tok.Total != 30 {
		t.Fatalf("run tokens = %+v, want 20/10/30 over two streamed model calls", tok)
	}
	if other := logger.ForWorker(3).Tokens(); other != tok {
		t.Fatalf("worker loggers must share the run totals: %+v vs %+v", other, tok)
	}
}

func TestToolOutcome(t *testing.T) {
	cases := []struct {
		out any
		ok  bool
		err string
	}{
		{map[string]any{"files": []any{}}, true, ""},
		{map[string]any{"success": true}, true, ""},
		{map[string]any{"success": false, "error": "名称重复"}, false, "名称重复"},
		{map[string]any{"error": "file not found"}, false, "file not found"},
		{"plain text", true, ""},
	}
	for _, c := range cases {
		ok, err := toolOutcome(c.out)
		if ok != c.ok || err != c.err {
			t.Errorf("toolOutcome(%v) = %v %q, want %v %q", c.out, ok, err, c.ok, c.err)
		}
	}
}

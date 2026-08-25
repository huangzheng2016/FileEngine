package agent

import (
	"testing"
	"time"

	"FileEngine/internal/db"
)

func recvBusEvent(t *testing.T, ch chan db.Event) db.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return db.Event{}
	}
}

// A logger with a nil repo is safe as long as the buffer never reaches the
// flush threshold; these tests stay well below it.
func TestLoggerLogMessagePublishesToBus(t *testing.T) {
	ch := db.SubscribeEvents(100)
	defer db.UnsubscribeEvents(100, ch)

	l := NewLogger(nil, 100)
	l.SetBatch(3)
	l.LogMessage("assistant", "final answer", 10, 20, 30)

	ev := recvBusEvent(t, ch)
	if ev.Type != db.EventMessage {
		t.Fatalf("event type: got %q, want %q", ev.Type, db.EventMessage)
	}
	p, ok := ev.Payload.(db.MessagePayload)
	if !ok {
		t.Fatalf("payload type: got %T", ev.Payload)
	}
	if p.BatchIndex != 3 || p.Role != "assistant" || p.Content != "final answer" ||
		p.PromptTokens != 10 || p.CompletionTokens != 20 || p.TotalTokens != 30 {
		t.Fatalf("payload: %+v", p)
	}
}

func TestLoggerLogToolCallDoesNotPublish(t *testing.T) {
	ch := db.SubscribeEvents(101)
	defer db.UnsubscribeEvents(101, ch)

	// Tool-call records are DB-only; live tool events come from the ADK event
	// loop (EmitToolCall/EmitToolResult) to avoid duplicates.
	l := NewLogger(nil, 101)
	l.LogToolCall("list_files", map[string]int{"limit": 1}, map[string]int{"total": 1})

	select {
	case ev := <-ch:
		t.Fatalf("unexpected event from LogToolCall: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestLoggerEmitHelpers(t *testing.T) {
	ch := db.SubscribeEvents(102)
	defer db.UnsubscribeEvents(102, ch)

	l := NewLogger(nil, 102)
	l.SetBatch(-1)

	l.EmitBatchStart("instruct")
	ev := recvBusEvent(t, ch)
	if p, ok := ev.Payload.(db.BatchStartPayload); ev.Type != db.EventBatchStart || !ok ||
		p.BatchIndex != -1 || p.Label != "instruct" {
		t.Fatalf("batch_start: type=%q payload=%+v", ev.Type, ev.Payload)
	}

	l.EmitToolCall("read_file", `{"path":"a.txt"}`)
	ev = recvBusEvent(t, ch)
	if p, ok := ev.Payload.(db.ToolCallPayload); ev.Type != db.EventToolCall || !ok ||
		p.ToolName != "read_file" || p.Input != `{"path":"a.txt"}` {
		t.Fatalf("tool_call: type=%q payload=%+v", ev.Type, ev.Payload)
	}

	l.EmitToolResult("read_file", `{"content":"x"}`, true)
	ev = recvBusEvent(t, ch)
	if p, ok := ev.Payload.(db.ToolResultPayload); ev.Type != db.EventToolResult || !ok ||
		p.ToolName != "read_file" || !p.OK {
		t.Fatalf("tool_result: type=%q payload=%+v", ev.Type, ev.Payload)
	}

	l.EmitMessage("assistant", "thinking...")
	ev = recvBusEvent(t, ch)
	if p, ok := ev.Payload.(db.MessagePayload); ev.Type != db.EventMessage || !ok ||
		p.Role != "assistant" || p.Content != "thinking..." || p.TotalTokens != 0 {
		t.Fatalf("message: type=%q payload=%+v", ev.Type, ev.Payload)
	}
}

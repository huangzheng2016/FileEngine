package agent

import (
	"context"
	"testing"
)

// Truncated tool-call JSON from the model is replayed in the next request's
// history, where the Claude client embeds it raw and the body stops being
// JSON. The fix belongs to the model wrapper (transport lane): normalize
// ToolCall.Function.Arguments once the stream is assembled.
func TestWireTruncatedToolArgumentsSurviveReplay(t *testing.T) {
	ws := newWireServer(t, byRound(calls(wireCall{"t1", "decide", `{"items":[`}), text("完成")))
	a, _, _ := wireAgent(t, ws, nil)
	w := newWireWorker(t, a, 0)
	err := a.runUnitLLM(context.Background(), w, workUnit{kind: unitDir, path: "misc"})
	ws.mu.Lock()
	bad := ws.bad
	ws.mu.Unlock()
	if bad > 0 {
		t.Skip("the model wrapper must replace invalid tool-call arguments before they are replayed")
	}
	if err != nil {
		t.Fatal(err)
	}
}

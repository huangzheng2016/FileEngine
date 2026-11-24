package agent

import (
	"context"
	"testing"
)

// Truncated tool-call JSON from the model is replayed in the next request's
// history, where the Claude client embeds it raw and the body stops being
// JSON; the Claude wrapper replays invalid arguments as an empty object.
func TestWireTruncatedToolArgumentsSurviveReplay(t *testing.T) {
	ws := newWireServer(t, byRound(calls(wireCall{"t1", "decide", `{"items":[`}), text("完成")))
	a, _, _ := wireAgent(t, ws, nil)
	w := newWireWorker(t, a, 0)
	err := a.runUnitLLM(context.Background(), w, workUnit{kind: unitDir, path: "misc"})
	ws.mu.Lock()
	bad := ws.bad
	ws.mu.Unlock()
	if bad > 0 {
		t.Fatalf("%d request bodies were not valid JSON", bad)
	}
	if err != nil {
		t.Fatal(err)
	}
}

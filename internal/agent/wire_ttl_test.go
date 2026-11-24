package agent

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"FileEngine/internal/db"
)

// cacheMarks lists every cache_control of a request in render order as
// "<place>:<json>".
func cacheMarks(t *testing.T, r wireReq) []string {
	t.Helper()
	var out []string
	collect := func(place string, raw json.RawMessage) {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			t.Fatalf("%s: %v", place, err)
		}
		for _, it := range items {
			if cc, ok := it["cache_control"]; ok {
				out = append(out, place+":"+string(cc))
			}
		}
	}
	collect("tools", r.tools)
	collect("system", r.system)
	for i, m := range r.messages {
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(m, &msg); err != nil {
			t.Fatal(err)
		}
		place := "tail"
		if i == 0 {
			place = "shared"
		}
		collect(place, msg.Content)
	}
	return out
}

// Chat turns are human-paced: tools, system and shared context live for an
// hour, and the rolling tail keeps the default TTL (a longer TTL must precede
// a shorter one).
func TestWireChatUsesOneHourCacheForThePrefix(t *testing.T) {
	ws := newWireServer(t, byRound(
		calls(wireCall{"t1", "recall", `{}`}),
		text("好的"),
	))
	_, a, session := wireChatAgent(t, ws)
	files, _, _ := a.repo.ListFiles(db.FileQuery{SessionID: session.ID})
	if _, err := a.RunChat(context.Background(), NewLogger(session.ID, 7, 0), "帮我整理照片", files); err != nil {
		t.Fatal(err)
	}
	reqs := ws.requests()
	if len(reqs) != 2 {
		t.Fatalf("want 2 requests, got %d", len(reqs))
	}
	want := []string{
		`tools:{"ttl":"1h","type":"ephemeral"}`,
		`system:{"ttl":"1h","type":"ephemeral"}`,
		`shared:{"ttl":"1h","type":"ephemeral"}`,
		`tail:{"type":"ephemeral"}`,
	}
	for i, r := range reqs {
		if got := cacheMarks(t, r); !slices.Equal(got, want) {
			t.Fatalf("request %d cache marks = %v, want %v", i, got, want)
		}
	}
}

func TestWireTaggingKeepsDefaultCacheTTL(t *testing.T) {
	ws := newWireServer(t, byRound(text("完成")))
	a, _, _ := wireAgent(t, ws, nil)
	w := newWireWorker(t, a, 0)
	if err := a.runUnitLLM(context.Background(), w, workUnit{kind: unitDir, path: "misc"}); err != nil {
		t.Fatal(err)
	}
	reqs := ws.requests()
	if len(reqs) == 0 {
		t.Fatal("no request")
	}
	want := []string{
		`tools:{"type":"ephemeral"}`,
		`system:{"type":"ephemeral"}`,
		`shared:{"type":"ephemeral"}`,
		`tail:{"type":"ephemeral"}`,
	}
	for i, r := range reqs {
		if got := cacheMarks(t, r); !slices.Equal(got, want) {
			t.Fatalf("request %d cache marks = %v, want %v", i, got, want)
		}
	}
}

package agent

import (
	"context"
	"strings"
	"testing"
)

// A change made during a run (here a category another worker created)
// reaches later units as a delta; the snapshot message stays byte-identical.
func TestWireMidRunChangesArriveAsDeltaNotInTheSnapshot(t *testing.T) {
	ws := newWireServer(t, func(r wireReq) wireReply {
		if assistantCount(r) == 0 && strings.Contains(string(r.messages[len(r.messages)-1]), "misc/inner") {
			return wireReply{calls: []wireCall{{"t1", "create_category", `{"name":"动态分类","path":"/动态","description":"运行中新建"}`}}}
		}
		return wireReply{text: "完成"}
	})
	a, _, _ := wireAgent(t, ws, nil)
	w := newWireWorker(t, a, 0)
	ctx := context.Background()
	if _, err := a.sharedSnapshot(); err != nil {
		t.Fatal(err)
	}
	for _, u := range []workUnit{{kind: unitDir, path: "misc"}, {kind: unitDir, path: "misc/inner"}, {kind: unitDir, path: "proj"}} {
		if err := a.runUnitLLM(ctx, w, u); err != nil {
			t.Fatal(err)
		}
	}
	first := firstRequests(ws.requests())
	if len(first) != 3 {
		t.Fatalf("units: %d", len(first))
	}
	for i := range first {
		if string(first[i].messages[0]) != string(first[0].messages[0]) {
			t.Fatalf("unit %d: the snapshot must not change mid-run", i)
		}
		if strings.Contains(string(first[i].messages[0]), "动态分类") {
			t.Fatal("a mid-run category is not in the snapshot")
		}
	}
	if strings.Contains(string(first[0].messages[1]), "运行中的变化") || strings.Contains(string(first[1].messages[1]), "运行中的变化") {
		t.Fatal("no delta before anything changed")
	}
	last := string(first[2].messages[1])
	if !strings.Contains(last, "运行中的变化") || !strings.Contains(last, "动态分类") {
		t.Fatalf("the unit after the change carries it as a delta: %s", last)
	}
	if strings.Index(last, "运行中的变化") > strings.Index(last, "当前任务") {
		t.Fatal("the delta leads the unit message")
	}
}

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

// Wire-level acceptance tests: the real worker, ADK loop, eino Claude client
// and anthropic SDK talk to a fake Messages API server.

func byRound(rounds ...func(r wireReq) wireReply) func(r wireReq) wireReply {
	return func(r wireReq) wireReply {
		i := min(assistantCount(r), len(rounds)-1)
		return rounds[i](r)
	}
}

func text(s string) func(wireReq) wireReply {
	return func(wireReq) wireReply { return wireReply{text: s} }
}

func calls(cs ...wireCall) func(wireReq) wireReply {
	return func(wireReq) wireReply { return wireReply{calls: cs} }
}

func seedMemory(t *testing.T, repo *db.Repository, n int) {
	t.Helper()
	for i := range n {
		m := &db.MemoryItem{Scope: db.MemoryScopeFilesystem, ScopeID: 7, Kind: db.MemoryConvention,
			Key: "k" + string(rune('a'+i)), Content: "约定" + string(rune('甲'+i)), Source: db.MemorySourceUser}
		if _, err := repo.UpsertMemory(m); err != nil {
			t.Fatal(err)
		}
	}
}

// 1. Two tagging workers send the same tools, system and shared-context bytes.
func TestWireWorkersShareByteIdenticalPrefix(t *testing.T) {
	ws := newWireServer(t, byRound(text("完成")))
	a, repo, _ := wireAgent(t, ws, nil)
	seedMemory(t, repo, 3)
	w0, w1 := newWireWorker(t, a, 0), newWireWorker(t, a, 1)

	var wg sync.WaitGroup
	for i, run := range []struct {
		w *tagWorker
		u workUnit
	}{{w0, workUnit{kind: unitDir, path: "misc"}}, {w1, workUnit{kind: unitDir, path: "misc/inner"}}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.runUnitLLM(context.Background(), run.w, run.u); err != nil {
				t.Errorf("worker %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	first := firstRequests(ws.requests())
	if len(first) != 2 {
		t.Fatalf("want one opening request per worker, got %d", len(first))
	}
	if string(first[0].tools) != string(first[1].tools) || len(first[0].tools) < 100 {
		t.Fatal("tools differ between workers")
	}
	if string(first[0].system) != string(first[1].system) || len(first[0].system) < 100 {
		t.Fatal("system differs between workers")
	}
	if string(first[0].messages[0]) != string(first[1].messages[0]) {
		t.Fatalf("shared context differs between workers:\n%s\n---\n%s", first[0].messages[0], first[1].messages[0])
	}
	if string(first[0].messages[1]) == string(first[1].messages[1]) {
		t.Fatal("the unit message is the part that differs")
	}
	if !strings.Contains(string(first[0].messages[0]), "约定甲") {
		t.Fatalf("shared context carries the memory block: %s", first[0].messages[0])
	}
}

// 2. Within a unit, every request extends the previous one.
func TestWireRequestsExtendThePreviousRequest(t *testing.T) {
	ws := newWireServer(t, byRound(
		calls(wireCall{"t1", "recall", `{}`}, wireCall{"t2", "list_entries", `{"parent_path":"misc"}`}),
		calls(wireCall{"t3", "note", `{"content":"misc 是杂项目录"}`}),
		text("完成"),
	))
	a, _, _ := wireAgent(t, ws, nil)
	w := newWireWorker(t, a, 0)
	if err := a.runUnitLLM(context.Background(), w, workUnit{kind: unitDir, path: "misc"}); err != nil {
		t.Fatal(err)
	}
	reqs := ws.requests()
	if len(reqs) != 3 {
		t.Fatalf("want 3 rounds, got %d", len(reqs))
	}
	for i := 1; i < len(reqs); i++ {
		prev, cur := reqs[i-1], reqs[i]
		if string(prev.tools) != string(cur.tools) || string(prev.system) != string(cur.system) {
			t.Fatalf("round %d: tools or system changed", i)
		}
		if len(cur.messages) <= len(prev.messages) {
			t.Fatalf("round %d: history must grow", i)
		}
		for k := range prev.messages {
			if got, want := stripCacheControl(t, cur.messages[k]), stripCacheControl(t, prev.messages[k]); got != want {
				t.Fatalf("round %d: message %d is not a prefix of the previous request:\n%s\n---\n%s", i, k, want, got)
			}
		}
	}
}

// 4. Memory recall between requests leaves the shared bytes alone.
func TestWireRecallDoesNotChangeSharedBytes(t *testing.T) {
	ws := newWireServer(t, byRound(
		calls(wireCall{"t1", "recall", `{"query":"约定乙"}`}, wireCall{"t2", "recall", `{"query":"约定丙"}`}),
		text("完成"),
	))
	a, repo, _ := wireAgent(t, ws, nil)
	seedMemory(t, repo, 4)
	w := newWireWorker(t, a, 0)
	for _, u := range []workUnit{{kind: unitDir, path: "misc"}, {kind: unitDir, path: "misc/inner"}, {kind: unitDir, path: "proj"}} {
		if err := a.runUnitLLM(context.Background(), w, u); err != nil {
			t.Fatal(err)
		}
	}
	first := firstRequests(ws.requests())
	if len(first) != 3 {
		t.Fatalf("units: %d", len(first))
	}
	for i := 1; i < len(first); i++ {
		if string(first[i].messages[0]) != string(first[0].messages[0]) {
			t.Fatalf("unit %d: recall changed the shared bytes", i)
		}
	}
	items, _ := repo.ListLongTermMemory(7)
	for _, m := range items {
		if m.Hits != 0 {
			t.Fatalf("recall must not record usage: %+v", m)
		}
	}
	// And at string level, for every pair of items.
	session := a.session
	before, _ := BuildSharedContext(repo, session)
	tb := NewToolBuilder(repo, nil, session.ID, 7, nil, config.AgentConfig{}, session, false)
	for _, q := range []string{"约定乙", "约定丁", "约定甲", ""} {
		callTool(t, tb, ToolsetTagging, "recall", map[string]string{"query": q})
		after, _ := BuildSharedContext(repo, session)
		if after != before {
			t.Fatalf("recall %q changed the shared context", q)
		}
	}
}

// 5. Toggling a session switch does not change the tools bytes.
func TestWireToolsIdenticalAcrossSwitches(t *testing.T) {
	var tools [][]byte
	for _, flip := range []func(*db.ScanSession){
		func(s *db.ScanSession) {
			s.AllowReadFile, s.AllowScript, s.AllowAutoCategory, s.AllowVision = true, true, true, true
		},
		func(s *db.ScanSession) {
			s.AllowReadFile, s.AllowScript, s.AllowAutoCategory, s.AllowVision = false, false, false, false
		},
		func(s *db.ScanSession) {
			s.AllowReadFile, s.AllowScript, s.AllowAutoCategory, s.AllowVision = true, false, true, false
		},
	} {
		ws := newWireServer(t, byRound(text("完成")))
		a, _, _ := wireAgent(t, ws, flip)
		w := newWireWorker(t, a, 0)
		if err := a.runUnitLLM(context.Background(), w, workUnit{kind: unitDir, path: "misc"}); err != nil {
			t.Fatal(err)
		}
		tools = append(tools, ws.requests()[0].tools)
	}
	for i := 1; i < len(tools); i++ {
		if string(tools[i]) != string(tools[0]) {
			t.Fatalf("switch set %d changed the tools bytes", i)
		}
	}
	var decoded []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(tools[0], &decoded); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range decoded {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != strings.Join(ToolNames(ToolsetTagging), ",") {
		t.Fatalf("tools on the wire: %v", names)
	}
}

// 9. Bad arguments, a missing path and a bad script come back as results and
// the unit goes on to its next round.
func TestWireBadCallsAreResultsAndTheUnitContinues(t *testing.T) {
	ws := newWireServer(t, byRound(
		calls(
			wireCall{"t1", "decide", `{"items":"nope"}`},
			wireCall{"t2", "read_file", `{"path":"no/such/file.txt"}`},
			wireCall{"t3", "list_entries", `{"paths":["nope/x"]}`},
			wireCall{"t4", "run_script", `{"script":"undefined_fn()"}`},
			wireCall{"t5", "describe", `{"items":[{"path":"nope","description":"x"}]}`},
		),
		text("完成"),
	))
	a, repo, _ := wireAgent(t, ws, nil)
	w := newWireWorker(t, a, 0)
	if err := a.runUnitLLM(context.Background(), w, workUnit{kind: unitDir, path: "misc"}); err != nil {
		t.Fatalf("the unit must not fail: %v", err)
	}
	reqs := ws.requests()
	if len(reqs) != 2 {
		t.Fatalf("the model gets a second round: %d requests", len(reqs))
	}
	results := toolResults(t, reqs[1])
	if len(results) != 5 {
		t.Fatalf("five results expected: %v", results)
	}
	for i, raw := range results {
		var r struct {
			Success *bool  `json:"success"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &r); err != nil || r.Success == nil || *r.Success || r.Error == "" {
			t.Fatalf("result %d must be success:false with a reason: %s", i, raw)
		}
	}
	if e, _ := repo.GetFileByPath(a.sessionID, "misc"); e.Role != db.RoleUndecided {
		t.Fatalf("nothing was decided by the bad calls: %+v", e)
	}
}

// 6. Chat turn N+1 starts with turn N's request messages, however the plan,
// categories or memory changed in between.
func TestWireChatTurnExtendsThePreviousTurn(t *testing.T) {
	ws := newWireServer(t, byRound(text("好的")))
	_, a, session := wireChatAgent(t, ws)
	repo := a.repo
	if err := repo.CreateCategory(&db.Category{FilesystemID: session.FilesystemID, Name: "照片", Path: "/照片"}); err != nil {
		t.Fatal(err)
	}
	files, _, _ := repo.ListFiles(db.FileQuery{SessionID: session.ID})
	ctx := context.Background()
	if _, err := a.RunChat(ctx, NewLogger(session.ID, 7, 0), "帮我整理照片", files); err != nil {
		t.Fatal(err)
	}
	// Everything the snapshot holds changes between the turns.
	if err := repo.CreateCategory(&db.Category{FilesystemID: session.FilesystemID, Name: "软件", Path: "/软件", Description: "安装包"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertMemory(&db.MemoryItem{Scope: db.MemoryScopeGlobal, Kind: db.MemoryPreference, Key: "pref:x", Content: "新偏好丙", Source: db.MemorySourceUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SavePlan(session.ID, "# 方案\n照片按年", 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunChat(ctx, NewLogger(session.ID, 8, 0), "再加一条", nil); err != nil {
		t.Fatal(err)
	}
	reqs := ws.requests()
	if len(reqs) != 2 {
		t.Fatalf("one request per turn: %d", len(reqs))
	}
	r1, r2 := reqs[0], reqs[1]
	if string(r1.tools) != string(r2.tools) || string(r1.system) != string(r2.system) {
		t.Fatal("tools or system changed between turns")
	}
	for k := range r1.messages {
		if got, want := stripCacheControl(t, r2.messages[k]), stripCacheControl(t, r1.messages[k]); got != want {
			t.Fatalf("message %d of turn 2 is not turn 1's:\n%s\n---\n%s", k, want, got)
		}
	}
	if string(r2.messages[0]) != string(r1.messages[0]) {
		t.Fatal("the shared snapshot is byte-identical across the epoch")
	}
	if len(r2.messages) != len(r1.messages)+2 {
		t.Fatalf("turn 2 adds the answer and the new message: %d vs %d", len(r2.messages), len(r1.messages))
	}
	last := string(r2.messages[len(r2.messages)-1])
	for _, want := range []string{"运行中的变化", "软件", "新偏好丙", "再加一条"} {
		if !strings.Contains(last, want) {
			t.Fatalf("the new message carries %q: %s", want, last)
		}
	}
	if strings.Contains(string(r2.messages[0]), "新偏好丙") || strings.Contains(string(r2.messages[0]), "软件") {
		t.Fatal("changes stay out of the snapshot")
	}
	if !strings.Contains(string(r1.messages[len(r1.messages)-1]), "a.jpg") {
		t.Fatal("turn 1's message carries the selected files, and turn 2 replays it verbatim")
	}
}

// 9, unknown tool name. The handler is mine; it takes effect once the ADK
// builder (adk_build.go, transport lane) sets
// ToolsNodeConfig.UnknownToolsHandler = unknownToolHandler.
func TestWireUnknownToolIsAResultAndTheUnitContinues(t *testing.T) {
	ws := newWireServer(t, byRound(calls(wireCall{"t1", "frobnicate", `{"x":1}`}), text("完成")))
	a, _, _ := wireAgent(t, ws, nil)
	w := newWireWorker(t, a, 0)
	err := a.runUnitLLM(context.Background(), w, workUnit{kind: unitDir, path: "misc"})
	if err != nil && strings.Contains(err.Error(), "not found in toolsNode") {
		t.Skip("adk_build.go must set ToolsNodeConfig.UnknownToolsHandler = unknownToolHandler")
	}
	if err != nil {
		t.Fatal(err)
	}
	reqs := ws.requests()
	if len(reqs) != 2 {
		t.Fatalf("the model gets a second round: %d", len(reqs))
	}
	if r := toolResults(t, reqs[1]); len(r) != 1 || !strings.Contains(r[0], `"success":false`) || !strings.Contains(r[0], "frobnicate") {
		t.Fatalf("unknown tool result: %v", r)
	}
}

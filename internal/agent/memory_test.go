package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func mem(id uint, scope, kind, content, path string, hits int, pinned bool) db.MemoryItem {
	return db.MemoryItem{ID: id, Scope: scope, Kind: kind, Key: fmt.Sprintf("k%d", id), Content: content, Path: path,
		Hits: hits, Pinned: pinned, UpdatedAt: time.Unix(int64(id), 0)}
}

func TestBuildMemoryBlockOrderAndBudget(t *testing.T) {
	fs, gl := db.MemoryScopeFilesystem, db.MemoryScopeGlobal
	items := []db.MemoryItem{
		mem(1, fs, db.MemoryFact, "事实一条", "", 50, false),
		mem(2, fs, db.MemoryCorrection, "psd 放设计", "", 1, false),
		mem(3, gl, db.MemoryPreference, "文件夹用中文名", "", 0, false),
		mem(4, fs, db.MemoryConvention, "置顶约定", "", 0, true),
		mem(5, fs, db.MemoryCorrection, "Design 下的 psd 应放到 /设计", "Design", 0, false),
		mem(6, db.MemoryScopeSession, db.MemoryNote, "会话笔记不进长期块", "", 0, false),
	}
	out := BuildMemoryBlock(items, []string{"Design/2023/x"}, 0)
	if !strings.HasPrefix(out, "## 记忆") {
		t.Fatalf("heading: %q", out)
	}
	if strings.Contains(out, "会话笔记") {
		t.Fatal("session notes must not be in the long-term block")
	}
	order := []string{"Design 下的 psd", "置顶约定", "文件夹用中文名", "psd 放设计", "事实一条"}
	last := -1
	for _, w := range order {
		i := strings.Index(out, w)
		if i <= last {
			t.Fatalf("order broken at %q:\n%s", w, out)
		}
		last = i
	}
	if !strings.Contains(out, "### 与当前位置相关") || !strings.Contains(out, "【纠正】") || !strings.Contains(out, "【偏好·通用】") {
		t.Fatalf("labels:\n%s", out)
	}

	// Budget: many items get cut with a recall hint; path-related ones survive.
	var many []db.MemoryItem
	for i := 1; i <= 200; i++ {
		many = append(many, mem(uint(i), fs, db.MemoryConvention, strings.Repeat("约定内容", 10)+fmt.Sprint(i), "", 0, false))
	}
	many = append(many, mem(999, fs, db.MemoryCorrection, "相关纠正", "Photos", 0, false))
	out = BuildMemoryBlock(many, []string{"photos/2020"}, 300)
	if !strings.Contains(out, "相关纠正") {
		t.Fatal("path-related item must be included regardless of the budget")
	}
	if !strings.Contains(out, "可用 recall 查询") {
		t.Fatalf("truncation hint missing:\n%s", out)
	}
	if got := estimateTokens(out); got > 300+60 {
		t.Fatalf("block exceeds budget: %d tokens", got)
	}
	shown := strings.Count(out, "【约定】")
	var left int
	fmt.Sscanf(out[strings.Index(out, "另有 ")+len("另有 "):], "%d", &left)
	if shown+left != 200 {
		t.Fatalf("shown %d + left %d must be 200", shown, left)
	}

	if BuildMemoryBlock(nil, nil, 0) != "" {
		t.Fatal("empty")
	}
	// Default budget is ~1.5K tokens.
	out = BuildMemoryBlock(many, nil, 0)
	if got := estimateTokens(out); got > memoryBlockTokenBudget || got < memoryBlockTokenBudget-200 {
		t.Fatalf("default budget use: %d tokens", got)
	}
}

func TestCorrectionMemory(t *testing.T) {
	f := db.FileEntry{Name: "logo.PSD", FileType: "file", ParentPath: "Design/2023/brand"}
	m := CorrectionMemory(f, "/图片/logo.PSD", "设计/源文件/logo.PSD", 3)
	if m == nil {
		t.Fatal("expected a correction")
	}
	if m.Scope != db.MemoryScopeFilesystem || m.ScopeID != 3 || m.Kind != db.MemoryCorrection || m.Source != db.MemorySourceCorrection {
		t.Fatalf("fields: %+v", m)
	}
	if m.Key != "correction:.psd@/Design/2023" || m.Path != "Design/2023" {
		t.Fatalf("key/path: %+v", m)
	}
	want := "`/Design/2023` 下的 `*.psd` 应放到 `/设计/源文件`，而不是 `/图片`"
	if m.Content != want {
		t.Fatalf("content: %q", m.Content)
	}

	if CorrectionMemory(f, "/a/logo.PSD", "", 3) != nil {
		t.Fatal("clearing a target teaches nothing")
	}
	if CorrectionMemory(f, "/a/logo.PSD", "/a/logo-v2.PSD", 3) != nil {
		t.Fatal("a rename in the same directory teaches nothing")
	}
	d := CorrectionMemory(db.FileEntry{Name: "proj", FileType: "directory", ParentPath: "."}, "", "/代码/proj", 3)
	if d == nil || d.Key != "correction:dir@/" || strings.Contains(d.Content, "而不是") || !strings.Contains(d.Content, "目录") {
		t.Fatalf("directory correction from root: %+v", d)
	}
	n := CorrectionMemory(db.FileEntry{Name: "Makefile", FileType: "file", ParentPath: "src"}, "", "/代码/Makefile", 3)
	if n == nil || n.Key != "correction:noext@/src" {
		t.Fatalf("no-extension correction: %+v", n)
	}
}

func brainTestBuilder(t *testing.T) (*ToolBuilder, *db.Repository) {
	t.Helper()
	repo := setupToolTestRepo(t)
	tb := NewToolBuilder(repo, nil, 1, 7, NewLogger(nil, 1, 0), config.AgentConfig{}, &db.ScanSession{ID: 1, FilesystemID: 7, AllowScript: true}, false)
	tb.RunID = 42
	return tb, repo
}

func TestMemoryTools(t *testing.T) {
	tb, repo := brainTestBuilder(t)
	ctx := context.Background()

	out, err := tb.remember(ctx, &RememberInput{Scope: "filesystem", Kind: "convention", Key: "ext:.PSD", Content: "psd 放 /设计", Path: "/Design/"})
	if err != nil || !out.Success || !out.Created {
		t.Fatalf("remember: %+v %v", out, err)
	}
	m, _ := repo.GetMemory(out.ID)
	if m.ScopeID != 7 || m.Key != "ext:.psd" || m.Path != "Design" || m.Source != db.MemorySourceAgent {
		t.Fatalf("stored: %+v", m)
	}
	for _, bad := range []*RememberInput{
		{Scope: "session", Kind: "fact", Key: "k", Content: "c"},
		{Scope: "global", Kind: "note", Key: "k", Content: "c"},
		{Scope: "global", Kind: "fact", Key: "", Content: "c"},
		{Scope: "global", Kind: "fact", Key: "k", Content: strings.Repeat("长", 400)},
	} {
		out, err := tb.remember(ctx, bad)
		if err != nil || out.Success || out.Error == "" {
			t.Fatalf("invalid remember %+v must be a recoverable rejection: %+v %v", bad, out, err)
		}
	}
	if _, err := tb.remember(ctx, &RememberInput{Scope: "global", Kind: "preference", Key: "pref:lang", Content: "文件夹用中文名"}); err != nil {
		t.Fatal(err)
	}

	rec, err := tb.recall(ctx, &RecallInput{Query: "PSD"})
	if err != nil || len(rec.Items) != 1 || rec.Items[0].Key != "ext:.psd" {
		t.Fatalf("recall: %+v %v", rec, err)
	}
	m, _ = repo.GetMemory(out.ID)
	if m.Hits != 1 {
		t.Fatalf("recall must bump hits: %+v", m)
	}
	rec, _ = tb.recall(ctx, &RecallInput{PathPrefix: "Design/2023/logo"})
	if len(rec.Items) != 1 {
		t.Fatalf("path-prefix recall (item path is an ancestor): %+v", rec)
	}
	rec, _ = tb.recall(ctx, &RecallInput{PathPrefix: "Other"})
	if len(rec.Items) != 0 {
		t.Fatalf("unrelated path: %+v", rec)
	}
	rec, _ = tb.recall(ctx, &RecallInput{})
	if len(rec.Items) != 2 {
		t.Fatalf("empty query lists all scopes: %+v", rec)
	}
}

func TestNotesAreSharedAcrossWorkers(t *testing.T) {
	a, repo := brainTestBuilder(t)
	b := NewToolBuilder(repo, nil, 1, 7, NewLogger(nil, 1, 1), config.AgentConfig{}, &db.ScanSession{ID: 1, FilesystemID: 7}, false)
	ctx := context.Background()

	if out, err := a.note(ctx, &NoteInput{Content: "Projects/foo 是 Go 项目根目录，整体归类", Path: "Projects/foo"}); err != nil || !out.Success {
		t.Fatalf("note: %+v %v", out, err)
	}
	// The same content is one note (idempotent key).
	if out, _ := a.note(ctx, &NoteInput{Content: "Projects/foo 是 Go 项目根目录，整体归类"}); out.Created {
		t.Fatal("identical note must update, not duplicate")
	}
	if out, _ := a.note(ctx, &NoteInput{Content: "  "}); out.Success {
		t.Fatal("empty note must be rejected")
	}

	block, err := b.TakeNewNotes()
	if err != nil || !strings.Contains(block, "## 其他 worker 的发现") || !strings.Contains(block, "Go 项目根目录") {
		t.Fatalf("worker b must see a's note: %q %v", block, err)
	}
	if again, _ := b.TakeNewNotes(); again != "" {
		t.Fatalf("cursor must advance: %q", again)
	}
	if own, _ := a.TakeNewNotes(); own != "" {
		t.Fatalf("a worker does not get its own notes back: %q", own)
	}
	if _, err := b.note(ctx, &NoteInput{Content: "照片按 年/月 命名"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.TakeNewNotes(); !strings.Contains(got, "年/月") || strings.Contains(got, "Go 项目") {
		t.Fatalf("a sees only b's new note: %q", got)
	}
}

func TestPlanToolsAndTargetException(t *testing.T) {
	tb, repo := brainTestBuilder(t)
	ctx := context.Background()
	seedFile(t, repo, 1, "in/a.jpg", "file")
	seedCategory(t, repo, 7, "照片", "/照片")

	// Without a plan, targets outside every category are rejected (result, not error).
	out, err := tb.setTarget(ctx, &SetTargetInput{Path: "in/a.jpg", NewPath: "/归档/2023/a.jpg"})
	if err != nil || out.Success || !strings.Contains(out.Error, "不在任何分类路径下") || !strings.Contains(out.Error, "/照片") {
		t.Fatalf("outside-category target: %+v %v", out, err)
	}
	// A sibling prefix is not "under" a category.
	if out, _ := tb.setTarget(ctx, &SetTargetInput{Path: "in/a.jpg", NewPath: "/照片集/a.jpg"}); out.Success {
		t.Fatal("/照片集 is not under /照片")
	}
	if out, _ := tb.setTarget(ctx, &SetTargetInput{Path: "in/a.jpg", NewPath: "照片/2023/a.jpg"}); !out.Success || out.NewPath != "/照片/2023/a.jpg" {
		t.Fatalf("relative target under a category is normalized and accepted: %+v", out)
	}

	rp, _ := tb.readPlan(ctx, &ReadPlanInput{})
	if rp.Exists {
		t.Fatal("no plan yet")
	}
	if w, _ := tb.writePlan(ctx, &WritePlanInput{Content: "  "}); w.Success {
		t.Fatal("empty plan must be rejected")
	}
	w, err := tb.writePlan(ctx, &WritePlanInput{Content: "# 方案\n- /归档/{年}"})
	if err != nil || !w.Success || w.Version != 1 || w.Status != db.PlanDraft {
		t.Fatalf("write_plan: %+v %v", w, err)
	}
	rp, _ = tb.readPlan(ctx, &ReadPlanInput{})
	if !rp.Exists || rp.Version != 1 || !strings.Contains(rp.Content, "/归档/{年}") {
		t.Fatalf("read_plan: %+v", rp)
	}
	// With a plan, the plan's own destinations outside categories are allowed.
	if out, _ := tb.setTarget(ctx, &SetTargetInput{Path: "in/a.jpg", NewPath: "/归档/2023/a.jpg"}); !out.Success {
		t.Fatalf("plan exception: %+v", out)
	}
	if _, err := repo.ApprovePlan(1); err != nil {
		t.Fatal(err)
	}
	w, _ = tb.writePlan(ctx, &WritePlanInput{Content: "# 方案 v2"})
	if w.Version != 2 || w.Status != db.PlanDraft {
		t.Fatalf("agent rewrite returns the plan to draft: %+v", w)
	}
}

func TestSetTargetToolReportsCollision(t *testing.T) {
	tb, repo := brainTestBuilder(t)
	ctx := context.Background()
	seedFile(t, repo, 1, "a/x.jpg", "file")
	seedFile(t, repo, 1, "b/x.jpg", "file")
	seedCategory(t, repo, 7, "照片", "/照片")

	if out, _ := tb.setTarget(ctx, &SetTargetInput{Path: "a/x.jpg", NewPath: "/照片/x.jpg"}); out.NewPath != "/照片/x.jpg" || out.Guidance != "" {
		t.Fatalf("first: %+v", out)
	}
	out, err := tb.setTarget(ctx, &SetTargetInput{Path: "b/x.jpg", NewPath: "/照片/x.jpg"})
	if err != nil || !out.Success || out.NewPath != "/照片/x (2).jpg" || !strings.Contains(out.Guidance, "已自动改为 /照片/x (2).jpg") {
		t.Fatalf("collision: %+v %v", out, err)
	}
}

func TestScriptSetCategoryKeepsStructure(t *testing.T) {
	tb, repo := scriptTestBuilder(t, nil)
	scriptSeedFile(t, repo, 1, "incoming/trip/day1/p.jpg", "file")
	scriptSeedFile(t, repo, 1, "incoming/trip/day2/p.jpg", "file")

	out := mustRunScript(t, tb, `
a := set_category("incoming/trip/day1/p.jpg", "照片", "incoming")
b := set_category("incoming/trip/day2/p.jpg", "照片", ".")
c := set_category("incoming/photo1.jpg", "照片", "docs")
result = {a: a, b: b, c_err: is_error(c)}`)
	v := resultValue(t, out)
	if v["a"] != "/organized/photos/trip/day1/p.jpg" || v["b"] != "/organized/photos/incoming/trip/day2/p.jpg" || v["c_err"] != true {
		t.Fatalf("relative_to: %+v", v)
	}

	// Default: relative to the unit root when set, else the file name.
	tb.SetUnitRoot("/incoming/trip/")
	out = mustRunScript(t, tb, `
a := set_category("incoming/trip/day1/p.jpg", "照片")
b := set_category("incoming/photo2.jpg", "照片")
result = {a: a, b: b}`)
	v = resultValue(t, out)
	if v["a"] != "/organized/photos/day1/p.jpg" || v["b"] != "/organized/photos/photo2.jpg" {
		t.Fatalf("unit-root default: %+v", v)
	}
	// Two files landing on the same name get distinct targets.
	out = mustRunScript(t, tb, `result = {x: set_category("incoming/trip/day2/p.jpg", "照片", "incoming/trip/day2")}`)
	if v := resultValue(t, out); v["x"] != "/organized/photos/p.jpg" {
		t.Fatalf("free name: %+v", v)
	}
	out = mustRunScript(t, tb, `result = {x: set_category("incoming/trip/day1/p.jpg", "照片", "incoming/trip/day1")}`)
	if v := resultValue(t, out); v["x"] != "/organized/photos/p (2).jpg" {
		t.Fatalf("collision in script: %+v", v)
	}
}

func TestApplyMemoryOps(t *testing.T) {
	repo := setupToolTestRepo(t)
	userItem := &db.MemoryItem{Scope: db.MemoryScopeFilesystem, ScopeID: 7, Kind: db.MemoryPreference, Key: "u", Content: "用户写的", Source: db.MemorySourceUser}
	agentItem := &db.MemoryItem{Scope: db.MemoryScopeFilesystem, ScopeID: 7, Kind: db.MemoryFact, Key: "a", Content: "旧事实", Source: db.MemorySourceAgent}
	dupItem := &db.MemoryItem{Scope: db.MemoryScopeFilesystem, ScopeID: 7, Kind: db.MemoryFact, Key: "dup", Content: "重复", Source: db.MemorySourceAgent}
	foreign := &db.MemoryItem{Scope: db.MemoryScopeFilesystem, ScopeID: 8, Kind: db.MemoryFact, Key: "f", Content: "别的文件系统", Source: db.MemorySourceAgent}
	note := &db.MemoryItem{Scope: db.MemoryScopeSession, ScopeID: 1, Kind: db.MemoryNote, Key: "n", Content: "笔记", Source: db.MemorySourceAgent}
	for _, m := range []*db.MemoryItem{userItem, agentItem, dupItem, foreign, note} {
		if _, err := repo.UpsertMemory(m); err != nil {
			t.Fatal(err)
		}
	}
	res, err := ApplyMemoryOps(repo, 7, []MemoryOp{
		{Op: "ADD", Kind: "convention", Key: "ext:.psd", Content: "psd 放 /设计"},
		{Op: "add", Scope: "global", Kind: "preference", Key: "pref:lang", Content: "中文目录名"},
		{Op: "UPDATE", ID: agentItem.ID, Content: "新事实"},
		{Op: "DELETE", ID: dupItem.ID},
		{Op: "NOOP"},
		{Op: "UPDATE", ID: userItem.ID, Content: "改用户的"},
		{Op: "DELETE", ID: foreign.ID},
		{Op: "DELETE", ID: note.ID},
		{Op: "ADD", Scope: "session", Kind: "fact", Key: "x", Content: "c"},
		{Op: "ADD", Kind: "fact", Key: "", Content: "c"},
		{Op: "UPDATE", ID: 9999, Content: "x"},
		{Op: "MERGE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 || res.Updated != 1 || res.Deleted != 1 || res.Noop != 1 || len(res.Rejected) != 7 {
		t.Fatalf("result: %+v", res)
	}
	if m, _ := repo.GetMemory(agentItem.ID); m.Content != "新事实" || m.Source != db.MemorySourceReflection {
		t.Fatalf("update: %+v", m)
	}
	if _, err := repo.GetMemory(dupItem.ID); err == nil {
		t.Fatal("delete")
	}
	for _, id := range []uint{userItem.ID, foreign.ID, note.ID} {
		if _, err := repo.GetMemory(id); err != nil {
			t.Fatalf("protected item %d must survive", id)
		}
	}
	if m, _ := repo.GetMemory(userItem.ID); m.Content != "用户写的" {
		t.Fatal("user item must not change")
	}
	g, _ := repo.SearchMemory(db.MemorySearch{Query: "中文目录名", Scopes: []db.MemoryScopeRef{{Scope: db.MemoryScopeGlobal}}})
	if len(g) != 1 || g[0].Source != db.MemorySourceReflection {
		t.Fatalf("global add: %+v", g)
	}
}

func TestSharedContextRender(t *testing.T) {
	repo := setupToolTestRepo(t)
	session := &db.ScanSession{FilesystemID: 7, Goal: "照片按年份整理，项目保持完整"}
	if err := repo.CreateSession(session); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateCategory(&db.Category{FilesystemID: 7, Name: "照片", Path: "/照片", Description: "所有照片\n和视频", Structure: "{年}/{原文件夹名}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SavePlan(session.ID, "# 方案\n照片按年", 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertMemory(&db.MemoryItem{Scope: db.MemoryScopeFilesystem, ScopeID: 7, Kind: db.MemoryCorrection, Key: "c", Content: "截图不算照片", Path: "Photos"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []db.FileEntry{
		{ScanSessionID: session.ID, OriginalPath: "Photos", Name: "Photos", FileType: "directory", Description: "手机相册导出"},
		{ScanSessionID: session.ID, OriginalPath: "Photos/2023", Name: "2023", FileType: "directory"},
		{ScanSessionID: session.ID, OriginalPath: "Photos/2023/trip", Name: "trip", FileType: "directory", Description: "2023 旅行"},
	} {
		e := e
		if err := repo.CreateFile(&e); err != nil {
			t.Fatal(err)
		}
	}

	out, err := BuildSharedContext(repo, session, []string{"Photos/2023/trip"}, "## 其他 worker 的发现\n\n- 某发现\n")
	if err != nil {
		t.Fatal(err)
	}
	sections := []string{"## 用户目标\n\n照片按年份整理", "## 整理方案（第 1 版，草稿，用户尚未确认）", "## 记忆", "截图不算照片", "## 其他 worker 的发现", "## 分类体系", "放什么：所有照片 和视频", "目录结构：{年}/{原文件夹名}", "## 上级目录", "- Photos：手机相册导出", "- Photos/2023/trip：2023 旅行"}
	last := -1
	for _, s := range sections {
		i := strings.Index(out, s)
		if i <= last {
			t.Fatalf("section %q missing or out of order:\n%s", s, out)
		}
		last = i
	}
	if strings.Contains(out, "Photos/2023：") {
		t.Fatal("ancestors without description are skipped")
	}

	empty := RenderSharedContext(SharedContext{})
	if empty != "" {
		t.Fatalf("empty context renders nothing: %q", empty)
	}
}

func TestSystemPromptCarriesGoal(t *testing.T) {
	s := &db.ScanSession{Goal: "  照片按年份整理 ", AllowAutoCategory: true}
	got := systemPromptForSession(config.AgentConfig{}, s)
	if !strings.HasPrefix(got, "## 用户目标\n\n照片按年份整理\n\n") {
		t.Fatalf("goal must lead the system prompt: %q", got[:60])
	}
	custom := systemPromptForSession(config.AgentConfig{SystemPrompt: "自定义"}, s)
	if custom != "## 用户目标\n\n照片按年份整理\n\n自定义" {
		t.Fatalf("custom prompt keeps the goal: %q", custom)
	}
	if ip := instructPromptForSession(config.AgentConfig{}, s); !strings.HasPrefix(ip, "## 用户目标") || !strings.Contains(ip, "意图不明确时先问") || !strings.Contains(ip, "remember") {
		t.Fatal("instruct prompt: goal, clarifying questions, remember")
	}
	if !strings.Contains(DefaultSystemPrompt, "以目标为准") || !strings.Contains(DefaultSystemPrompt, "note") {
		t.Fatal("default prompt must state goal precedence and teach note")
	}
}

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func entryNamed(name, typ string) model.FileEntry {
	return model.FileEntry{OriginalPath: "d/" + name, Name: name, FileType: typ}
}

func TestComputeSignals(t *testing.T) {
	dir := &model.FileEntry{OriginalPath: "work/api", Name: "api", Depth: 2}
	kids := []model.FileEntry{entryNamed("go.mod", "file"), entryNamed("main.go", "file"), entryNamed("util.go", "file"), entryNamed(".git", "directory"), entryNamed("README", "file")}
	s := ComputeSignals(dir, kids, 40, 1<<20)
	if m := s.strongMarker(); m == nil || m.Kind != markerCode {
		t.Fatalf("go.mod is a strong code marker: %+v", s.Markers)
	}
	if s.Files != 4 || s.Dirs != 1 || s.Exts[0] != (ExtCount{Ext: "go", Count: 2}) {
		t.Fatalf("counts / histogram: %+v", s)
	}
	out := s.Render()
	for _, want := range []string{"共 40 个文件", "go.mod（Go 项目）", "扩展名：go 2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q: %s", want, out)
		}
	}
	app := ComputeSignals(&model.FileEntry{Name: "Sketch.app"}, nil, 0, 0)
	if m := app.strongMarker(); m == nil || m.Kind != markerBundle {
		t.Fatalf(".app bundle: %+v", app.Markers)
	}
	inst := ComputeSignals(&model.FileEntry{Name: "tool"}, []model.FileEntry{entryNamed("setup.exe", "file"), entryNamed("a.dll", "file")}, 0, 0)
	if len(inst.Markers) != 1 || inst.Markers[0].Kind != markerSoftware || inst.strongMarker() != nil {
		t.Fatalf("installer is a weak marker: %+v", inst.Markers)
	}
}

func TestDetectVersionGroups(t *testing.T) {
	kids := []model.FileEntry{
		entryNamed("FooApp-1.2.3-x64.exe", "file"), entryNamed("FooApp_1.3.0_setup.exe", "file"), entryNamed("fooapp v2.0.zip", "file"),
		entryNamed("Bar 2023", "directory"), entryNamed("Bar 2024", "directory"),
		entryNamed("IMG_0001.jpg", "file"), entryNamed("IMG_0002.jpg", "file"),
		entryNamed("Solo-1.0.dmg", "file"), entryNamed("report (1).zip", "file"), entryNamed("report.zip", "file"),
	}
	groups := DetectVersionGroups(kids)
	if len(groups) != 2 {
		t.Fatalf("groups: %+v", groups)
	}
	if groups[0].Name != "Bar" || len(groups[0].Members) != 2 || groups[0].Members[0].Version != "2023" {
		t.Fatalf("year versions: %+v", groups[0])
	}
	if !strings.EqualFold(groups[1].Name, "FooApp") || len(groups[1].Members) != 3 {
		t.Fatalf("software versions: %+v", groups[1])
	}
	decided := kids[0]
	decided.Role = db.RoleFile
	if g := DetectVersionGroups([]model.FileEntry{decided, kids[1]}); len(g) != 0 {
		t.Fatal("decided entries never join a group")
	}
}

// pipelineFixture: a scan tree with a project, a nested container, loose
// files, a version group, an out-of-scope file and junk.
func pipelineFixture(t *testing.T, goal string) (*AutoTagAgent, *db.Repository) {
	t.Helper()
	repo := setupToolTestRepo(t)
	session := &model.ScanSession{FilesystemID: 7, Goal: goal, ScopeExts: "", AllowScript: true, AllowAutoCategory: true}
	if err := repo.DB().Create(session).Error; err != nil {
		t.Fatal(err)
	}
	for _, c := range []model.Category{{FilesystemID: 7, Name: "代码", Path: "/代码"}, {FilesystemID: 7, Name: "文档", Path: "/文档"}, {FilesystemID: 7, Name: "软件", Path: "/软件"}} {
		c := c
		if err := repo.DB().Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []struct{ p, typ string }{
		{"proj", "directory"}, {"proj/go.mod", "file"}, {"proj/main.go", "file"},
		{"misc", "directory"}, {"misc/inner", "directory"}, {"misc/inner/deep.txt", "file"}, {"misc/a.pdf", "file"}, {"misc/b.pdf", "file"},
		{"Tool-1.0.zip", "file"}, {"Tool-2.0.zip", "file"}, {"readme.txt", "file"}, {".DS_Store", "file"},
	} {
		parent := ""
		if i := strings.LastIndex(e.p, "/"); i >= 0 {
			parent = e.p[:i]
		}
		f := &model.FileEntry{ScanSessionID: session.ID, OriginalPath: e.p, Name: e.p[strings.LastIndex(e.p, "/")+1:], FileType: e.typ, ParentPath: parent, Depth: strings.Count(e.p, "/")}
		if err := repo.DB().Create(f).Error; err != nil {
			t.Fatal(err)
		}
	}
	a := NewAutoTagAgent(repo, config.RemoteFSConfig{}, &model.ModelProvider{}, session, session.ID, NewLogger(session.ID, 1, 0), testCfg())
	return a, repo
}

func fakeWorkers(a *AutoTagAgent, decide func(tb *ToolBuilder, u workUnit), seen *[]workUnit, mu *sync.Mutex) func(int) (*tagWorker, func(), error) {
	return func(id int) (*tagWorker, func(), error) {
		logger := a.logger.ForWorker(id)
		tb := NewToolBuilder(a.repo, nil, a.sessionID, a.session.FilesystemID, logger, a.cfg, a.session, false)
		w := &tagWorker{id: id, logger: logger, tb: tb}
		w.run = func(ctx context.Context, w *tagWorker, u workUnit) error {
			mu.Lock()
			*seen = append(*seen, u)
			mu.Unlock()
			decide(w.tb, u)
			return nil
		}
		return w, func() {}, nil
	}
}

func TestPipelineTopDownUnits(t *testing.T) {
	a, repo := pipelineFixture(t, "项目保持完整")
	if err := a.prepass(); err != nil {
		t.Fatal(err)
	}
	if e, _ := repo.GetFileByPath(a.sessionID, ".DS_Store"); e.Role != db.RoleSkip {
		t.Fatalf("junk skipped up front: %+v", e)
	}
	var seen []workUnit
	var mu sync.Mutex
	decide := func(tb *ToolBuilder, u workUnit) {
		switch u.kind {
		case unitDir:
			_, _ = tb.decideTool(context.Background(), &DecideInput{Items: []DecideItem{{Path: u.path, Action: actionContainer, Reason: "分组目录"}}})
		case unitGroup:
			var items []DecideItem
			for _, f := range u.files {
				items = append(items, DecideItem{Path: f.OriginalPath, Action: actionMove, NewPath: "/文档/" + f.Name, Reason: "文档"})
			}
			out, _ := tb.decideTool(context.Background(), &DecideInput{Items: items})
			if !out.Success {
				t.Errorf("group targets: %+v", out)
			}
		case unitVersions:
			var items []DecideItem
			for _, m := range u.versions.Members {
				items = append(items, DecideItem{Path: m.Path, Action: actionMove, NewPath: "/软件/Tool/" + m.Version + "/" + m.Name, Reason: "版本"})
			}
			_, _ = tb.decideTool(context.Background(), &DecideInput{Items: items})
		}
	}
	if err := a.walk(context.Background(), fakeWorkers(a, decide, &seen, &mu)); err != nil {
		t.Fatal(err)
	}

	kinds := map[string][]string{}
	for _, u := range seen {
		kinds[u.kind] = append(kinds[u.kind], u.path)
	}
	for _, p := range kinds[unitDir] {
		if p == "proj" {
			t.Fatal("strong marker must auto-decide without an LLM unit")
		}
	}
	if e, _ := repo.GetFileByPath(a.sessionID, "proj"); e.Role != db.RoleUnit || e.NewPath != "/代码/proj" || !strings.Contains(e.Reason, "go.mod") {
		t.Fatalf("auto unit: %+v", e)
	}
	// misc is a dir unit; after it became a container, misc/inner and the
	// folder group of misc's loose files followed; the root's loose files
	// form one group; the two Tool zips one version group.
	if len(kinds[unitDir]) != 2 || len(kinds[unitGroup]) != 3 || len(kinds[unitVersions]) != 1 {
		t.Fatalf("units: %+v", kinds)
	}
	for _, u := range seen {
		if u.kind == unitGroup && u.path == "misc" && len(u.files) != 2 {
			t.Fatalf("one folder group per container: %+v", u)
		}
	}
	if b, _ := repo.CountUntaggedByType(a.sessionID); b.Total() != 0 {
		t.Fatalf("everything decided: %+v", b)
	}
	if len(a.Failures()) != 0 {
		t.Fatalf("no failures: %+v", a.Failures())
	}
}

func TestPipelineFailuresAndAutoContainer(t *testing.T) {
	a, repo := pipelineFixture(t, "")
	if err := a.prepass(); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Decide(a.sessionID, db.Decision{Path: "misc/inner/deep.txt", Role: db.RoleSkip}); err != nil {
		t.Fatal(err)
	}
	var seen []workUnit
	var mu sync.Mutex
	if err := a.walk(context.Background(), fakeWorkers(a, func(*ToolBuilder, workUnit) {}, &seen, &mu)); err != nil {
		t.Fatal(err)
	}
	for _, u := range seen {
		if u.path == "misc/inner" {
			t.Fatal("settled container must skip the LLM")
		}
	}
	if e, _ := repo.GetFileByPath(a.sessionID, "proj"); e.Role != db.RoleContainer {
		t.Fatalf("without goal/plan a marker is only a hint; undecided dirs continue as containers: %+v", e)
	}
	var labels []string
	for _, f := range a.Failures() {
		labels = append(labels, f.Target+"："+f.Err)
	}
	joined := strings.Join(labels, "\n")
	if !strings.Contains(joined, "按 container 继续") || !strings.Contains(joined, "遗漏") {
		t.Fatalf("undecided units are failures: %s", joined)
	}
}

func TestPipelineScopeSkipsOutOfScope(t *testing.T) {
	a, repo := pipelineFixture(t, "")
	a.session.ScopeExts = "pdf"
	if err := repo.DB().Model(a.session).Update("scope_exts", "pdf").Error; err != nil {
		t.Fatal(err)
	}
	if err := a.prepass(); err != nil {
		t.Fatal(err)
	}
	if e, _ := repo.GetFileByPath(a.sessionID, "readme.txt"); e.Role != db.RoleSkip {
		t.Fatalf("out of scope: %+v", e)
	}
	units, _ := a.expandContainer("")
	for _, u := range units {
		if u.kind == unitGroup || u.kind == unitVersions {
			t.Fatalf("root has no in-scope loose files: %+v", u)
		}
	}
}

func TestUnitMessagesShareByteIdenticalContext(t *testing.T) {
	a, repo := pipelineFixture(t, "照片按年份")
	if err := repo.CreateRule(&model.PlanRule{SessionID: a.sessionID, Name: "pdf", Exts: "pdf", Target: "文档"}); err != nil {
		t.Fatal(err)
	}
	logger := a.logger.ForWorker(0)
	w := &tagWorker{logger: logger, tb: NewToolBuilder(repo, nil, a.sessionID, 7, logger, a.cfg, a.session, false)}
	w2 := &tagWorker{logger: logger, tb: NewToolBuilder(repo, nil, a.sessionID, 7, logger, a.cfg, a.session, false)}
	m1, err := a.unitMessages(context.Background(), w, workUnit{kind: unitDir, path: "misc"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Decide(a.sessionID, db.Decision{Path: "readme.txt", Role: db.RoleFile, NewPath: "/文档/readme.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ApplyRules(a.sessionID, 0, false); err != nil {
		t.Fatal(err)
	}
	files, _ := repo.ListChildren(a.sessionID, "", 0)
	m2, err := a.unitMessages(context.Background(), w2, workUnit{kind: unitGroup, path: "", files: files})
	if err != nil {
		t.Fatal(err)
	}
	if m1[0].Content != m2[0].Content {
		t.Fatalf("shared context differs across units:\n%s\n---\n%s", m1[0].Content, m2[0].Content)
	}
	if m1[1].Content == m2[1].Content || !strings.Contains(m1[1].Content, "决定一个目录") || !strings.Contains(m2[1].Content, "散文件") {
		t.Fatal("per-unit message comes last and differs")
	}
	if strings.Contains(m1[0].Content, "misc") {
		t.Fatal("no per-unit data in the shared block")
	}
}

func TestToolOrderDeterministic(t *testing.T) {
	session := &model.ScanSession{AllowReadFile: true, AllowAutoCategory: true, AllowScript: true}
	names := func(kind ToolsetKind) []string {
		tb := NewToolBuilder(nil, nil, 1, 1, NewLogger(1, 0, 0), config.AgentConfig{}, session, false)
		tools, err := tb.BuildToolset(kind)
		if err != nil {
			t.Fatal(err)
		}
		return toolNames(t, tools)
	}
	first, second := names(ToolsetTagging), names(ToolsetTagging)
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatalf("order differs: %v vs %v", first, second)
	}
	want := "list_entries,read_file,view_image,decide,split_unit,describe,add_rule,apply_rules,run_script,create_category,note,recall"
	if strings.Join(first, ",") != want {
		t.Fatalf("tagging tools:\n%s\nwant\n%s", strings.Join(first, ","), want)
	}
	wantMain := want + ",update_category,delete_category,update_rule,delete_rule,read_plan,write_plan,remember"
	if got := strings.Join(names(ToolsetMain), ","); got != wantMain {
		t.Fatalf("main tools:\n%s\nwant\n%s", got, wantMain)
	}
	if strings.Join(ToolNames(ToolsetMain), ",") != wantMain {
		t.Fatal("ToolNames must match the built list")
	}
}

func TestDecisionToolsCategoryCheckAndReview(t *testing.T) {
	a, repo := pipelineFixture(t, "")
	tb := NewToolBuilder(repo, nil, a.sessionID, 7, a.logger, a.cfg, a.session, false)
	call := func(items ...map[string]any) map[string]any {
		return callTool(t, tb, ToolsetTagging, "decide", map[string]any{"items": items})
	}
	out := call(
		map[string]any{"path": "readme.txt", "action": "move", "new_path": "/归档/readme.txt", "reason": "x"},
		map[string]any{"path": "misc/a.pdf", "action": "move", "new_path": "文档/a.pdf", "reason": "文档", "confidence": 0.3},
		map[string]any{"path": "misc/b.pdf", "action": "move", "new_path": "/文档/a.pdf", "reason": "文档"},
	)
	failed, _ := out["failed"].([]any)
	if okOf(out) || out["ok"] != float64(2) || len(failed) != 1 || !strings.Contains(failed[0].(map[string]any)["error"].(string), "不在任何分类路径下") {
		t.Fatalf("decide: %+v", out)
	}
	renamed, _ := out["renamed"].([]any)
	if out["needs_review"] != float64(1) || len(renamed) != 1 || renamed[0].(map[string]any)["new_path"] != "/文档/a (2).pdf" {
		t.Fatalf("review + collision: %+v", out)
	}
	if e, _ := repo.GetFileByPath(a.sessionID, "misc/a.pdf"); !e.NeedsReview || e.Reason != "文档" {
		t.Fatalf("stored: %+v", e)
	}
	d := call(map[string]any{"path": "misc", "action": "unit", "new_path": "/文档/misc", "reason": "整体"})
	if okOf(d) || !strings.Contains(d["failed"].([]any)[0].(map[string]any)["error"].(string), "override") {
		t.Fatalf("unit over decided children needs override: %+v", d)
	}
	d = call(map[string]any{"path": "misc", "action": "unit", "new_path": "/文档/misc", "reason": "整体", "override": true})
	if !okOf(d) || len(d["overridden"].([]any)) != 2 {
		t.Fatalf("override: %+v", d)
	}
	out = call(map[string]any{"path": "misc/a.pdf", "action": "move", "new_path": "/文档/a.pdf", "reason": "x"})
	if okOf(out) || !strings.Contains(out["failed"].([]any)[0].(map[string]any)["error"].(string), "split_unit") {
		t.Fatalf("under unit: %+v", out)
	}
	sp := callTool(t, tb, ToolsetTagging, "split_unit", map[string]string{"path": "misc"})
	if !okOf(sp) || sp["released"] != float64(4) {
		t.Fatalf("split: %+v", sp)
	}
	big := make([]map[string]any, 201)
	for i := range big {
		big[i] = map[string]any{"path": "x", "action": "skip", "reason": "x"}
	}
	if r := call(big...); okOf(r) || errText(r) == "" {
		t.Fatal("batch cap")
	}
}

func TestDecideRequiresReasonAndValidAction(t *testing.T) {
	a, repo := pipelineFixture(t, "")
	tb := NewToolBuilder(repo, nil, a.sessionID, 7, a.logger, a.cfg, a.session, false)
	out := callTool(t, tb, ToolsetTagging, "decide", map[string]any{"items": []map[string]any{
		{"path": "readme.txt", "action": "move", "new_path": "/文档/readme.txt", "reason": "  "},
		{"path": "misc/a.pdf", "action": "teleport", "reason": "x"},
		{"path": "misc/b.pdf", "action": "move", "reason": "x"},
		{"path": "Tool-1.0.zip", "action": "skip", "reason": "安装包不整理"},
	}})
	failed := out["failed"].([]any)
	if okOf(out) || out["ok"] != float64(1) || len(failed) != 3 {
		t.Fatalf("each bad item is reported, the good one is written: %+v", out)
	}
	if e, _ := repo.GetFileByPath(a.sessionID, "readme.txt"); e.Role != db.RoleUndecided {
		t.Fatalf("no placeholder reason is written: %+v", e)
	}
	if e, _ := repo.GetFileByPath(a.sessionID, "Tool-1.0.zip"); e.Role != db.RoleSkip || e.Reason != "安装包不整理" {
		t.Fatalf("skip: %+v", e)
	}
	undo := callTool(t, tb, ToolsetTagging, "decide", map[string]any{"items": []map[string]any{{"path": "Tool-1.0.zip", "action": "undo", "reason": "改主意"}}})
	if !okOf(undo) {
		t.Fatalf("undo: %+v", undo)
	}
	if e, _ := repo.GetFileByPath(a.sessionID, "Tool-1.0.zip"); e.Role != db.RoleUndecided {
		t.Fatalf("undo clears the decision: %+v", e)
	}
}

func TestSurveyNeverDecides(t *testing.T) {
	a, repo := pipelineFixture(t, "")
	tb := NewToolBuilder(repo, nil, a.sessionID, 7, a.logger, a.cfg, a.session, false)
	tb.RunKind = db.RunPhaseSurvey
	for name, args := range map[string]any{
		"decide":      map[string]any{"items": []map[string]any{{"path": "readme.txt", "action": "skip", "reason": "x"}}},
		"split_unit":  map[string]string{"path": "misc"},
		"describe":    map[string]any{"items": []map[string]string{{"path": "misc", "description": "x"}}},
		"apply_rules": map[string]any{},
	} {
		out := callTool(t, tb, ToolsetMain, name, args)
		if okOf(out) || !strings.Contains(errText(out), "调研阶段") {
			t.Fatalf("%s must be refused in a survey run: %+v", name, out)
		}
	}
	if e, _ := repo.GetFileByPath(a.sessionID, "readme.txt"); e.Role != db.RoleUndecided {
		t.Fatalf("nothing decided: %+v", e)
	}
	// Categories, rules and the plan stay open.
	mustOK(t, callTool(t, tb, ToolsetMain, "add_rule", map[string]any{"name": "pdf", "exts": "pdf", "target": "文档/{parent}"}))
	mustOK(t, callTool(t, tb, ToolsetMain, "read_plan", map[string]any{}))
}

func TestListEntriesCompactAndSensitiveRead(t *testing.T) {
	a, repo := pipelineFixture(t, "")
	if err := repo.DB().Create(&model.FileEntry{ScanSessionID: a.sessionID, OriginalPath: "misc/evil\nname.txt", Name: "evil\nname.txt", FileType: "file", ParentPath: "misc"}).Error; err != nil {
		t.Fatal(err)
	}
	tb := NewToolBuilder(repo, nil, a.sessionID, 7, a.logger, a.cfg, &model.ScanSession{ID: a.sessionID, AllowReadFile: true}, false)
	list := func(args map[string]any) map[string]any {
		return callTool(t, tb, ToolsetTagging, "list_entries", args)
	}
	out := list(map[string]any{"parent_path": "misc", "limit": 2})
	if !okOf(out) || out["has_more"] != true || out["next_cursor"] == nil || !strings.Contains(out["rows"].(string), "<data ") {
		t.Fatalf("paged TSV inside <data>: %+v", out)
	}
	rest := list(map[string]any{"parent_path": "misc", "limit": 2, "cursor": out["next_cursor"]})
	if !okOf(rest) || rest["has_more"] == true || out["rows"] == rest["rows"] {
		t.Fatalf("second keyset page: %+v", rest)
	}
	if bad := list(map[string]any{"parent_path": "misc", "cursor": 999999}); okOf(bad) || errText(bad) == "" {
		t.Fatalf("stale cursor is a recoverable result: %+v", bad)
	}
	all := list(map[string]any{"parent_path": "/misc/", "undecided": true})
	if rows := all["rows"].(string); !strings.Contains(rows, "evil name.txt\tf") || strings.Contains(rows, "misc/") {
		t.Fatalf("names sanitized and relative: %q", rows)
	}
	byPath := list(map[string]any{"paths": []string{"misc/a.pdf", "nope/x.txt", "misc/evil\nname.txt"}})
	if !okOf(byPath) || !strings.Contains(byPath["rows"].(string), "misc/a.pdf\tf") || !strings.Contains(byPath["rows"].(string), "misc/evil name.txt") || len(byPath["missing"].([]any)) != 1 {
		t.Fatalf("paths lookup: %+v", byPath)
	}
	if out := list(map[string]any{"paths": []string{"nope"}}); okOf(out) {
		t.Fatalf("all missing: %+v", out)
	}
	if out := list(map[string]any{"paths": []string{"misc"}, "parent_path": "misc"}); okOf(out) {
		t.Fatalf("paths and parent_path together: %+v", out)
	}
	r := callTool(t, tb, ToolsetTagging, "read_file", map[string]string{"path": "keys/id_rsa"})
	if okOf(r) || !strings.Contains(errText(r), "敏感") {
		t.Fatalf("sensitive files are never read: %+v", r)
	}
	if r := callTool(t, tb, ToolsetTagging, "read_file", map[string]string{"path": "../x"}); okOf(r) {
		t.Fatalf("a bad path is a result, not a Go error: %+v", r)
	}
	off := NewToolBuilder(repo, nil, a.sessionID, 7, a.logger, a.cfg, &model.ScanSession{ID: a.sessionID, AllowReadFile: false}, false)
	if r := callTool(t, off, ToolsetTagging, "read_file", map[string]string{"path": "readme.txt"}); okOf(r) || !strings.Contains(errText(r), "未开启读取文件内容") {
		t.Fatalf("switch off: %+v", r)
	}
}

func TestScriptDecisionBuiltins(t *testing.T) {
	a, repo := pipelineFixture(t, "")
	a.session.AllowScript = true
	tb := NewToolBuilder(repo, nil, a.sessionID, 7, a.logger, a.cfg, a.session, false)
	if err := repo.CreateRule(&model.PlanRule{SessionID: a.sessionID, Name: "pdf", Exts: "pdf", Target: "文档/{parent}"}); err != nil {
		t.Fatal(err)
	}
	out := mustRunScript(t, tb, `
s := signals("proj")
dirs := list_dirs()
n := apply_rule("pdf")
d := decide_dir("proj", "unit", "/代码/proj", "Go 项目")
bad := decide_dir("proj/main.go", "skip", "", "已在整体内")
r := set_targets([{path: "readme.txt", new_path: "/文档/readme.txt", reason: "说明", confidence: 0.9}])
noReason := set_targets([{path: "Tool-1.0.zip", new_path: "/软件/Tool-1.0.zip"}])
result = {marker: s.markers[0].name, dirs: len(dirs), n: n, role: d.role, bad: is_error(bad), ok: r.ok, nr: noReason.failed[0]["error"]}`)
	v := resultValue(t, out)
	if v["marker"] != "go.mod" || v["dirs"] != float64(2) || v["n"] != float64(2) || v["role"] != "unit" || v["bad"] != true || v["ok"] != float64(1) {
		t.Fatalf("builtins: %+v", v)
	}
	if !strings.Contains(v["nr"].(string), "reason") {
		t.Fatalf("a script decision needs a reason: %+v", v)
	}
	if e, _ := repo.GetFileByPath(a.sessionID, "Tool-1.0.zip"); e.Role != db.RoleUndecided {
		t.Fatalf("no placeholder reason is written: %+v", e)
	}
	if e, _ := repo.GetFileByPath(a.sessionID, "misc/a.pdf"); e.NewPath != "/文档/misc/a.pdf" || e.Reason != "规则：pdf" {
		t.Fatalf("apply_rule: %+v", e)
	}
}

func TestRuleTools(t *testing.T) {
	a, repo := pipelineFixture(t, "")
	tb := NewToolBuilder(repo, nil, a.sessionID, 7, a.logger, a.cfg, a.session, false)
	add := func(args map[string]any) map[string]any { return callTool(t, tb, ToolsetMain, "add_rule", args) }
	if out := add(map[string]any{"name": "x", "exts": "pdf", "target": "归档/{year}"}); okOf(out) || !strings.Contains(errText(out), "分类") {
		t.Fatalf("agent rule outside categories: %+v", out)
	}
	if out := add(map[string]any{"name": "x", "exts": "pdf", "target": "文档/{nope}"}); okOf(out) {
		t.Fatal("unknown variable")
	}
	out := add(map[string]any{"name": "pdf", "exts": "pdf", "target": "文档/{parent}"})
	if !okOf(out) || out["matched_preview"] != float64(2) || out["rule"] != "pdf" {
		t.Fatalf("add: %+v", out)
	}
	if dup := add(map[string]any{"name": "pdf", "exts": "doc", "target": "文档"}); okOf(dup) {
		t.Fatalf("duplicate name: %+v", dup)
	}
	pv := callTool(t, tb, ToolsetMain, "apply_rules", map[string]any{"preview": true})
	if !okOf(pv) || pv["rules"].([]any)[0].(map[string]any)["matched"] != float64(2) {
		t.Fatalf("preview: %+v", pv)
	}
	if u := callTool(t, tb, ToolsetMain, "update_rule", map[string]any{"name": "pdf", "new_name": "PDF 文档", "name_regex": "^a"}); !okOf(u) {
		t.Fatalf("update: %+v", u)
	}
	if rules, _ := repo.ListRules(a.sessionID); rules[0].Exts != "pdf" || rules[0].NameRegex != "^a" {
		t.Fatalf("only the given fields change: %+v", rules[0])
	}
	if u := callTool(t, tb, ToolsetMain, "update_rule", map[string]any{"name": "PDF 文档", "clear": []string{"name_regex"}}); !okOf(u) {
		t.Fatalf("clear: %+v", u)
	}
	if rules, _ := repo.ListRules(a.sessionID); rules[0].NameRegex != "" {
		t.Fatalf("clear removes the condition: %+v", rules[0])
	}
	if u := callTool(t, tb, ToolsetMain, "update_rule", map[string]any{"name": "nope"}); okOf(u) {
		t.Fatalf("unknown rule: %+v", u)
	}
	ap := callTool(t, tb, ToolsetMain, "apply_rules", map[string]any{"rule": "PDF 文档"})
	r0 := ap["rules"].([]any)[0].(map[string]any)
	if r0["matched"] != float64(2) || r0["name"] != "PDF 文档" {
		t.Fatalf("apply: %+v", ap)
	}
	mustOK(t, callTool(t, tb, ToolsetMain, "delete_rule", map[string]any{"name": "PDF 文档"}))
	if d := callTool(t, tb, ToolsetMain, "delete_rule", map[string]any{"name": "PDF 文档"}); okOf(d) {
		t.Fatal("delete twice")
	}
}

func TestTaggingRulesNeedPathPrefix(t *testing.T) {
	a, repo := pipelineFixture(t, "")
	tb := NewToolBuilder(repo, nil, a.sessionID, 7, a.logger, a.cfg, a.session, false)
	tb.RunKind = db.RunPhaseTagging
	out := callTool(t, tb, ToolsetTagging, "add_rule", map[string]any{"name": "pdf", "exts": "pdf", "target": "文档/{parent}"})
	if okOf(out) || !strings.Contains(errText(out), "path_prefix") {
		t.Fatalf("a tagging rule without path_prefix: %+v", out)
	}
	mustOK(t, callTool(t, tb, ToolsetTagging, "add_rule", map[string]any{"name": "pdf", "exts": "pdf", "path_prefix": "misc", "target": "文档/{parent}"}))
}

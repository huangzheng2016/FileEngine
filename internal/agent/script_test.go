package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"FileEngine/internal/config"
	"FileEngine/internal/db"

	"github.com/cloudwego/eino/components/tool"
)

// scriptTestBuilder seeds a repo with files (ParentPath set, as the scanner
// would) and two categories, and returns a ToolBuilder wired for run_script
// tests (filesystem 7, session 1).
func scriptTestBuilder(t *testing.T, session *db.ScanSession) (*ToolBuilder, *db.Repository) {
	t.Helper()
	repo := setupToolTestRepo(t)
	if session == nil {
		session = &db.ScanSession{AllowScript: true}
	}
	scriptSeedFile(t, repo, 1, "incoming/photo1.jpg", "file")
	scriptSeedFile(t, repo, 1, "incoming/photo2.jpg", "file")
	scriptSeedFile(t, repo, 1, "docs/report.pdf", "file")
	scriptSeedFile(t, repo, 1, "docs", "directory")
	scriptSeedFile(t, repo, 1, "incoming", "directory")
	cat := &db.Category{FilesystemID: 7, Name: "照片", Path: "/organized/photos", AgentEditable: true}
	if err := repo.CreateCategory(cat); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	locked := &db.Category{FilesystemID: 7, Name: "锁定", Path: "/locked", AgentEditable: false}
	if err := repo.CreateCategory(locked); err != nil {
		t.Fatalf("seed locked category: %v", err)
	}
	tb := NewToolBuilder(repo, nil, 1, 7, NewLogger(nil, 1), config.AgentConfig{}, session, false)
	return tb, repo
}

func scriptSeedFile(t *testing.T, repo *db.Repository, sessionID uint, path, fileType string) {
	t.Helper()
	name := path
	parent := ""
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		name = path[idx+1:]
		parent = path[:idx]
	}
	f := &db.FileEntry{
		ScanSessionID: sessionID,
		OriginalPath:  path,
		ParentPath:    parent,
		Name:          name,
		FileType:      fileType,
	}
	if err := repo.CreateFile(f); err != nil {
		t.Fatalf("seed file %s: %v", path, err)
	}
}

func runScript(t *testing.T, tb *ToolBuilder, src string) (*RunScriptOutput, error) {
	t.Helper()
	return tb.runScript(context.Background(), &RunScriptInput{Script: src})
}

func mustRunScript(t *testing.T, tb *ToolBuilder, src string) *RunScriptOutput {
	t.Helper()
	out, err := runScript(t, tb, src)
	if err != nil {
		t.Fatalf("runScript: %v\nscript:\n%s", err, src)
	}
	return out
}

// resultValue decodes the script's `result` global from the tool output.
func resultValue(t *testing.T, out *RunScriptOutput) map[string]interface{} {
	t.Helper()
	if out.Value == "" {
		t.Fatal("script did not set a result value")
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out.Value), &m); err != nil {
		t.Fatalf("result value is not a JSON object: %v (%q)", err, out.Value)
	}
	return m
}

func hasToolNamed(tools []tool.BaseTool, name string) bool {
	for _, bt := range tools {
		info, err := bt.Info(context.Background())
		if err == nil && info.Name == name {
			return true
		}
	}
	return false
}

// --- builtin matrix: read side ---

func TestScriptReadBuiltins(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	out := mustRunScript(t, tb, `
q := query_files({parent_path: "incoming", file_type: "file"})
c := count_files({parent_path: "docs"})
info := get_file_info("incoming/photo1.jpg")
cats := get_categories()
s := sample_files({parent_path: "incoming"}, 1)
missing := get_file_info("nope/nothere")
result = {
	q_total: q.total,
	q_len: len(q.files),
	q_name: q.files[0].name,
	docs_count: c,
	info_path: info.original_path,
	info_parent: info.parent_path,
	cats_len: len(cats),
	cat0_editable: is_bool(cats[0].agent_editable),
	sample_len: len(s),
	missing_is_error: is_error(missing)
}`)
	v := resultValue(t, out)
	if v["q_total"].(float64) != 2 || v["q_len"].(float64) != 2 {
		t.Fatalf("query_files incoming: %+v", v)
	}
	if v["q_name"].(string) != "photo1.jpg" {
		t.Fatalf("query_files first item: %+v", v)
	}
	if v["docs_count"].(float64) != 1 {
		t.Fatalf("count_files docs: %+v", v)
	}
	if v["info_path"].(string) != "incoming/photo1.jpg" || v["info_parent"].(string) != "incoming" {
		t.Fatalf("get_file_info: %+v", v)
	}
	if v["cats_len"].(float64) != 2 {
		t.Fatalf("get_categories: %+v", v)
	}
	if v["cat0_editable"] != true {
		t.Fatalf("category agent_editable must be a bool: %+v", v)
	}
	if v["sample_len"].(float64) != 1 {
		t.Fatalf("sample_files n=1: %+v", v)
	}
	if v["missing_is_error"] != true {
		t.Fatalf("get_file_info on missing file must return an error value: %+v", v)
	}
}

// query_files pagination follows offset/limit like the list_files tool.
func TestScriptQueryFilesPagination(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	out := mustRunScript(t, tb, `
page1 := query_files({parent_path: "incoming", file_type: "file", limit: 1})
page2 := query_files({parent_path: "incoming", file_type: "file", limit: 1, offset: 1})
result = {t: page1.total, n1: page1.files[0].name, n2: page2.files[0].name}`)
	v := resultValue(t, out)
	if v["t"].(float64) != 2 || v["n1"] == v["n2"] {
		t.Fatalf("pagination: %+v", v)
	}
}

// print() output is captured per-run and returned, never written to stdout.
func TestScriptPrintAndResultContract(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	out := mustRunScript(t, tb, `
print("hello", "world")
print(1 + 2)
println("line")
result = "done"`)
	if out.Output != "hello world\n3\nline\n" {
		t.Fatalf("print capture: got %q", out.Output)
	}
	if out.Value != `"done"` {
		t.Fatalf("result value: got %q", out.Value)
	}
	if out.Truncated {
		t.Fatal("small run must not be truncated")
	}

	// The pre-declared result global takes plain assignment; an untouched
	// result yields no value.
	out2 := mustRunScript(t, tb, `result = 41 + 1`)
	if out2.Value != `42` {
		t.Fatalf("result assignment: got %q", out2.Value)
	}
	out3 := mustRunScript(t, tb, `print("no result here")`)
	if out3.Value != "" {
		t.Fatalf("untouched result must produce no value: got %q", out3.Value)
	}
}

// Unknown filter keys and bad argument shapes are loud errors, not silent
// pass-throughs.
func TestScriptBuiltinArgumentValidation(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	for _, src := range []string{
		`query_files({parrent_path: "incoming"})`, // typo key
		`query_files({tagged: "yes"})`,            // wrong type
		`get_file_info()`,                         // missing arg
		`set_target("a")`,                         // too few args
		`set_target("a", "b", "c")`,               // too many
		`set_target(1, "b")`,                      // wrong type
		`rename_category("照片", "")`,               // empty new name
	} {
		if _, err := runScript(t, tb, src); err == nil {
			t.Fatalf("script must fail loudly: %s", src)
		}
	}
}

// --- builtin matrix: mutating side (tool guard rails apply) ---

func TestScriptSetTargetBuiltin(t *testing.T) {
	tb, repo := scriptTestBuilder(t, nil)

	// Legal target: written. Illegal target: error value, nothing written.
	out := mustRunScript(t, tb, `
ok := set_target("incoming/photo1.jpg", "/organized/photos/photo1.jpg")
bad := set_target("incoming/photo2.jpg", "/../escape")
result = {ok: ok, bad_is_error: is_error(bad), bad_msg: string(bad)}`)
	v := resultValue(t, out)
	if v["ok"] != true || v["bad_is_error"] != true {
		t.Fatalf("set_target outcomes: %+v", v)
	}
	if !strings.Contains(v["bad_msg"].(string), "invalid new_path") {
		t.Fatalf("illegal-target message must name the validation failure: %+v", v)
	}

	f, _ := repo.GetFileByPath(1, "incoming/photo1.jpg")
	if f.NewPath != "/organized/photos/photo1.jpg" || f.Operation != "planned" {
		t.Fatalf("legal target must be written: %+v", f)
	}
	f2, _ := repo.GetFileByPath(1, "incoming/photo2.jpg")
	if f2.NewPath != "" || f2.Operation != "" {
		t.Fatalf("illegal target must not be written: %+v", f2)
	}

	// Clearing with "" works too.
	mustRunScript(t, tb, `set_target("incoming/photo1.jpg", "")`)
	f, _ = repo.GetFileByPath(1, "incoming/photo1.jpg")
	if f.NewPath != "" || f.Operation != "" {
		t.Fatalf("clear must empty the target: %+v", f)
	}
}

func TestScriptSetCategoryBuiltin(t *testing.T) {
	tb, repo := scriptTestBuilder(t, nil)
	out := mustRunScript(t, tb, `
ok := set_category("incoming/photo1.jpg", "照片")
missing_cat := set_category("incoming/photo2.jpg", "不存在")
missing_file := set_category("nope/x.jpg", "照片")
result = {ok: ok, cat_err: is_error(missing_cat), file_err: is_error(missing_file)}`)
	v := resultValue(t, out)
	if v["ok"] != true || v["cat_err"] != true || v["file_err"] != true {
		t.Fatalf("set_category outcomes: %+v", v)
	}
	f, _ := repo.GetFileByPath(1, "incoming/photo1.jpg")
	if f.NewPath != "/organized/photos/photo1.jpg" {
		t.Fatalf("set_category must target category path + file name: got %q", f.NewPath)
	}
}

func TestScriptRenameCategoryBuiltin(t *testing.T) {
	tb, repo := scriptTestBuilder(t, nil)
	out := mustRunScript(t, tb, `
ok := rename_category("照片", "相册")
locked := rename_category("锁定", "新名字")
missing := rename_category("不存在", "x")
result = {ok: ok, locked_err: is_error(locked), missing_err: is_error(missing)}`)
	v := resultValue(t, out)
	if v["ok"] != true || v["locked_err"] != true || v["missing_err"] != true {
		t.Fatalf("rename_category outcomes: %+v", v)
	}
	if _, err := repo.GetCategoryByName(7, "相册"); err != nil {
		t.Fatalf("renamed category must exist: %v", err)
	}
	if _, err := repo.GetCategoryByName(7, "锁定"); err != nil {
		t.Fatalf("user-managed category must not be renamed: %v", err)
	}
}

// create_category inside scripts enforces the identical guard rails as the
// tool: version guard first, then objective-duplicate rejection.
func TestScriptCreateCategoryBuiltinGuard(t *testing.T) {
	tb, repo := scriptTestBuilder(t, &db.ScanSession{AllowScript: true, AllowAutoCategory: true})

	// Without a preceding get_categories the version guard rejects.
	out := mustRunScript(t, tb, `
r := create_category("软件", "/organized/software", "apps")
result = {err: is_error(r), msg: string(r)}`)
	v := resultValue(t, out)
	if v["err"] != true || !strings.Contains(v["msg"].(string), "分类列表") {
		t.Fatalf("version guard must reject unseen-list creation: %+v", v)
	}

	// After get_categories the creation passes; a duplicate is rejected; and
	// the script's own creation updates the snapshot (back-to-back allowed).
	out = mustRunScript(t, tb, `
get_categories()
a := create_category("软件", "/organized/software", "apps")
dup := create_category("软件", "/elsewhere/software", "dupe")
b := create_category("游戏", "/organized/games", "games")
result = {a_ok: a.success, a_path: a.path, dup_err: is_error(dup), b_ok: b.success}`)
	v = resultValue(t, out)
	if v["a_ok"] != true || v["a_path"] != "/organized/software" {
		t.Fatalf("create after get_categories must pass: %+v", v)
	}
	if v["dup_err"] != true {
		t.Fatalf("objective duplicate must be rejected: %+v", v)
	}
	if v["b_ok"] != true {
		t.Fatalf("back-to-back creation must not be re-challenged: %+v", v)
	}
	if _, err := repo.GetCategoryByName(7, "软件"); err != nil {
		t.Fatalf("created category must persist: %v", err)
	}
}

// Without allow_auto_category the create_category builtin is not registered —
// scripts cannot bypass the session gate.
func TestScriptCreateCategoryGatedBySession(t *testing.T) {
	tb, _ := scriptTestBuilder(t, &db.ScanSession{AllowScript: true, AllowAutoCategory: false})
	if _, err := runScript(t, tb, `create_category("x", "/x", "d")`); err == nil {
		t.Fatal("create_category must be undefined when allow_auto_category is off")
	}
}

// --- guardrails ---

func TestScriptSizeGuard(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	tb.cfg.ScriptMaxSize = 16
	if _, err := runScript(t, tb, `result = "this script is definitely longer than sixteen bytes"`); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized script must be rejected: %v", err)
	}
	if _, err := runScript(t, tb, "   \n  "); err == nil {
		t.Fatal("empty script must be rejected")
	}
	// Just under the cap runs fine.
	if _, err := runScript(t, tb, `result = 1`); err != nil {
		t.Fatalf("small script must run: %v", err)
	}
}

func TestScriptTimeoutGuard(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	tb.cfg.ScriptTimeout = 1
	start := time.Now()
	_, err := runScript(t, tb, `for { x := 1 }`)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("infinite loop must die by timeout: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout abort took too long: %v", elapsed)
	}
}

// The print buffer is capped during the run: a print loop truncates instead
// of exhausting memory.
func TestScriptResultSizeGuard(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	tb.cfg.ScriptMaxResultSize = 64
	out := mustRunScript(t, tb, `
for i:=0; i<1000; i++ { print("0123456789") }
result = "ok"`)
	if len(out.Output) > 64 || !out.Truncated {
		t.Fatalf("print flood must be capped at 64 bytes: len=%d truncated=%v", len(out.Output), out.Truncated)
	}
}

// A huge result value is truncated to the cap as well.
func TestScriptValueSizeGuard(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	tb.cfg.ScriptMaxResultSize = 32
	out := mustRunScript(t, tb, `result = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
	if len(out.Value) > 32 || !out.Truncated {
		t.Fatalf("oversized result must be truncated: len=%d truncated=%v", len(out.Value), out.Truncated)
	}
}

// A runaway allocation loop dies by the alloc cap or the timeout — never by
// taking the process down — and the abort surfaces as a plain error.
func TestScriptAllocGuard(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	tb.cfg.ScriptTimeout = 3
	_, err := runScript(t, tb, `x := "" for { x += "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }`)
	if err == nil {
		t.Fatal("allocation bomb must be aborted (alloc cap or timeout)")
	}
}

// --- stdlib trimming ---

// Dangerous modules are not registered: os (exec/env/file/process) and fmt
// (process stdout) must be unavailable, as must tengo's file import.
func TestScriptDangerousModulesUnavailable(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	dangerous := []string{
		`os := import("os")`,
		`f := import("fmt")`,
		`e := import("enum2")`,         // unknown module
		`x := import("./local.tengo")`, // file import stays disabled
		`x := import("/etc/passwd")`,   // ditto, absolute
	}
	for _, src := range dangerous {
		if _, err := runScript(t, tb, src); err == nil {
			t.Fatalf("dangerous/unknown import must fail: %s", src)
		}
	}
}

// The whitelisted pure-function modules all work.
func TestScriptWhitelistedModulesWork(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	out := mustRunScript(t, tb, `
text := import("text")
math := import("math")
times := import("times")
json := import("json")
base64 := import("base64")
hex := import("hex")
rand := import("rand")
enum := import("enum")
doubled := enum.map([1, 2, 3], func(_, v) { return v * 2 })
result = {
	upper: text.to_upper("abc"),
	sqrt: math.sqrt(16.0),
	now_is_time: is_time(times.now()),
	encoded: string(json.encode({a: 1})),
	b64: base64.encode("hi"),
	hexed: hex.encode("hi"),
	rand_ok: rand.intn(1000) >= 0,
	mapped: doubled[1]
}`)
	v := resultValue(t, out)
	if v["upper"] != "ABC" || v["sqrt"] != 4.0 || v["encoded"] != `{"a":1}` ||
		v["b64"] != "aGk=" || v["hexed"] != "6869" || v["rand_ok"] != true ||
		v["now_is_time"] != true || v["mapped"] != 4.0 {
		t.Fatalf("whitelisted modules: %+v", v)
	}
}

// Scripts have no I/O beyond builtins: undefined identifiers fail at compile
// time (spot-check a few process-y names a model might try).
func TestScriptNoUnexpectedGlobals(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	for _, src := range []string{`exec("ls")`, `open("/etc/passwd")`, `http_get("http://x")`} {
		if _, err := runScript(t, tb, src); err == nil {
			t.Fatalf("unexpected global must not exist: %s", src)
		}
	}
}

// --- session switch & registration ---

// run_script registers only when the session allows it (default on).
func TestScriptToolRegistration(t *testing.T) {
	tb, _ := scriptTestBuilder(t, &db.ScanSession{AllowScript: true})
	tools, err := tb.BuildTools()
	if err != nil {
		t.Fatalf("BuildTools: %v", err)
	}
	if !hasToolNamed(tools, "run_script") {
		t.Fatal("run_script must be registered when AllowScript is true")
	}
	instructTools, err := tb.BuildInstructTools()
	if err != nil {
		t.Fatalf("BuildInstructTools: %v", err)
	}
	if !hasToolNamed(instructTools, "run_script") {
		t.Fatal("run_script must be registered for instruct when AllowScript is true")
	}

	tbOff, _ := scriptTestBuilder(t, &db.ScanSession{AllowScript: false})
	toolsOff, err := tbOff.BuildTools()
	if err != nil {
		t.Fatalf("BuildTools: %v", err)
	}
	if hasToolNamed(toolsOff, "run_script") {
		t.Fatal("run_script must not be registered when AllowScript is false")
	}
	instructOff, err := tbOff.BuildInstructTools()
	if err != nil {
		t.Fatalf("BuildInstructTools: %v", err)
	}
	if hasToolNamed(instructOff, "run_script") {
		t.Fatal("run_script must not be registered for instruct when AllowScript is false")
	}
}

// --- logging ---

// Script execution is persisted to agent_logs as one run_script entry
// carrying the (bounded) script source and its output; failures are logged
// with the error.
func TestScriptLoggedToAgentLogs(t *testing.T) {
	repo := setupToolTestRepo(t)
	scriptSeedFile(t, repo, 1, "a.txt", "file")
	logger := NewLogger(repo, 1)
	tb := NewToolBuilder(repo, nil, 1, 7, logger, config.AgentConfig{}, &db.ScanSession{AllowScript: true}, false)

	if _, err := tb.runScript(context.Background(), &RunScriptInput{Script: "print(\"logged line\")\nresult = 42"}); err != nil {
		t.Fatalf("runScript: %v", err)
	}
	if _, err := tb.runScript(context.Background(), &RunScriptInput{Script: `boom(`}); err == nil {
		t.Fatal("compile error expected")
	}
	logger.Flush()

	logs, total, err := repo.ListAgentLogs(db.LogQuery{SessionID: 1, ToolName: "run_script"})
	if err != nil || total != 2 {
		t.Fatalf("run_script logs: total=%d err=%v", total, err)
	}
	var okLog, errLog *db.AgentLog
	for i := range logs {
		if strings.Contains(logs[i].ToolOutput, "logged line") {
			okLog = &logs[i]
		}
		if strings.Contains(logs[i].ToolOutput, "error") {
			errLog = &logs[i]
		}
	}
	if okLog == nil {
		t.Fatalf("successful run must be logged with its output: %+v", logs)
	}
	if !strings.Contains(okLog.ToolInput, "logged line") || !strings.Contains(okLog.ToolOutput, "42") {
		t.Fatalf("log must carry script source and result: in=%q out=%q", okLog.ToolInput, okLog.ToolOutput)
	}
	if errLog == nil {
		t.Fatalf("failed run must be logged with the error: %+v", logs)
	}
}

// The tool name and description stay stable: agent_logs rows and SSE
// tool_call/tool_result events both key off the registered tool name, and the
// description is where the model learns the builtin API.
func TestScriptToolNameStable(t *testing.T) {
	tb, _ := scriptTestBuilder(t, nil)
	st, err := tb.buildScriptTool()
	if err != nil || st == nil {
		t.Fatalf("buildScriptTool: %v", err)
	}
	info, err := st.Info(context.Background())
	if err != nil {
		t.Fatalf("tool info: %v", err)
	}
	if info.Name != "run_script" {
		t.Fatalf("tool name: got %q, want run_script", info.Name)
	}
	if !strings.Contains(info.Desc, "query_files") || !strings.Contains(info.Desc, "set_target") {
		t.Fatal("tool description must document the builtins")
	}
}

// Example-shaped smoke test: the "类似 ipy" workflow end to end — query a
// directory, then bulk-classify everything in it.
func TestScriptBulkWorkflow(t *testing.T) {
	tb, repo := scriptTestBuilder(t, nil)
	out := mustRunScript(t, tb, `
q := query_files({parent_path: "incoming", file_type: "file", limit: 100})
moved := 0
for f in q.files {
	r := set_category(f.original_path, "照片")
	if !is_error(r) { moved += 1 }
}
print("classified", moved, "files")
result = {moved: moved}`)
	v := resultValue(t, out)
	if v["moved"].(float64) != 2 {
		t.Fatalf("bulk classify: %+v (output %q)", v, out.Output)
	}
	if !strings.Contains(out.Output, "classified 2 files") {
		t.Fatalf("print output: %q", out.Output)
	}
	for _, p := range []string{"incoming/photo1.jpg", "incoming/photo2.jpg"} {
		f, _ := repo.GetFileByPath(1, p)
		if f.NewPath != "/organized/photos/"+f.Name {
			t.Fatalf("%s must be planned under the category: %+v", p, f)
		}
	}
}

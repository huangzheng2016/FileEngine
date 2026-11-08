package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"FileEngine/internal/db"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/stdlib"
)

// run_script lets the agent run a Tengo script mid-run ("类似 ipy"): bulk
// queries and bulk category operations without one tool round-trip per file.
//
// Security model:
//   - stdlib is trimmed to pure-function modules (text/math/json/base64/hex/
//     rand/enum); os (exec/env/file/process) and fmt (writes to the process
//     stdout) are NOT registered, and tengo file import stays disabled.
//     times is excluded too: its sleep() blocks inside a VM builtin, and the
//     abort flag behind RunContext is only checked between VM instructions —
//     a sleeping script cannot be interrupted, bypassing script_timeout
//   - scripts have no I/O at all: every data access goes through the builtin
//     functions below, which reuse the tool layer's repository calls and
//     guard rails (ValidateFSPath, categoryGuard, M14 similarity semantics)
//   - each run is bounded by script_max_size (source), script_timeout
//     (wall clock, independent of the unlimited-by-default agent run timeout),
//     scriptMaxAllocs (object allocations — NOT bytes, see its comment) and
//     script_max_result_size (print output and return value, each)
//   - tengo's RunContext recovers VM panics into errors, so a panicking
//     script cannot take the worker down

type RunScriptInput struct {
	Script string `json:"script" jsonschema_description:"Tengo 脚本源码。用 print(...) 打印输出；给全局变量 result 赋值作为返回值。可用内置函数见工具描述"`
}

type RunScriptOutput struct {
	Output     string `json:"output"`              // captured print()/println() text
	Value      string `json:"value,omitempty"`     // JSON of the script's `result` global, when set
	Truncated  bool   `json:"truncated,omitempty"` // output or value hit script_max_result_size
	Error      string `json:"error,omitempty"`     // script compile/runtime/timeout failure, fed back for the model to fix
	DurationMs int64  `json:"duration_ms"`
}

// Fixed script limits (the configurable ones live in AgentConfig).
const (
	// scriptMaxAllocs caps tengo object allocations per run. It counts
	// allocations, NOT bytes: it stops many-small-object runaways (each
	// allocation-producing op decrements the counter), which comfortably
	// covers legitimate bulk queries (capped by scriptQueryMax per call).
	// It does NOT stop exponential-growth bombs: `x += x` doubles memory
	// each round with a single allocation per round, and one giant copy
	// cannot be interrupted mid-instruction — Go's out-of-memory is a fatal
	// error, not a recoverable panic, so the process would die inside the
	// timeout window. That residual availability risk is accepted (tengo has
	// no byte-level memory cap; real containment would need a subprocess
	// with rlimits) and documented in CLAUDE.md.
	scriptMaxAllocs     = 4_000_000
	scriptQueryDefault  = 200  // default query_files limit
	scriptQueryMax      = 1000 // hard cap on query_files limit
	scriptSampleDefault = 10   // default sample_files count
	scriptSampleMax     = 200  // hard cap on sample_files count
	scriptStdlibModules = "text, math, json, base64, hex, rand, enum"
)

// scriptLimits resolves the configurable guardrails with the same fallbacks
// as config.setDefaults (tests build AgentConfig literals that never pass
// through it).
func (tb *ToolBuilder) scriptLimits() (maxSize int, timeout time.Duration, maxResult int) {
	maxSize = tb.cfg.ScriptMaxSize
	if maxSize <= 0 {
		maxSize = 16 * 1024
	}
	secs := tb.cfg.ScriptTimeout
	if secs <= 0 {
		secs = 10
	}
	maxResult = tb.cfg.ScriptMaxResultSize
	if maxResult <= 0 {
		maxResult = 32 * 1024
	}
	return maxSize, time.Duration(secs) * time.Second, maxResult
}

// buildScriptTool returns the run_script tool when the session allows it, or
// (nil, nil) when gated off. allow_script defaults to ON at the DB level.
func (tb *ToolBuilder) buildScriptTool() (tool.BaseTool, error) {
	if !tb.session.AllowScript {
		return nil, nil
	}
	maxSize, timeout, maxResult := tb.scriptLimits()
	// The create_category builtin line carries the session gate note plus the
	// configured category policy verbatim (empty when no policy is set).
	createCatNote := map[bool]string{true: "", false: "（本会话未开启 allow_auto_category，不可用）"}[tb.session.AllowAutoCategory] + categoryPolicySuffix(tb.cfg.CategoryPolicy)
	desc := fmt.Sprintf(`运行一段 Tengo 脚本（类 Go 语法）做批量查询和批量分类操作，代替逐文件的工具调用。返回 print 输出和 result 全局变量的 JSON 值。

内置函数（全部围绕当前会话的文件与分类）：
- query_files({parent_path?, file_type?, tagged?, category_path?, offset?, limit?}) → {files: [...], total: N}，limit 默认 %d 上限 %d
- count_files({同上过滤条件}) → 整数
- get_file_info(path) → 文件详情 map；找不到返回 error 值
- get_categories() → 分类数组（name/path/structure/description/agent_editable）
- mark_tagged(path) → true；标记文件/目录为已处理（目录级联子项）。批量流程收尾必须对每个处理过的文件调用
- set_target(path, new_path) → true；new_path 传 "" 清除目标。与工具同样校验路径合法性
- set_category(path, category_name) → true；把文件归入指定分类（目标=分类路径+文件名）
- create_category(name, path, description) → 分类 map；受与 create_category 工具完全相同的护栏约束（需先 get_categories；同名/同路径拒绝）%s
- rename_category(name, new_name) → true；仅 agent_editable 的分类可改
- sample_files(n) 或 sample_files({过滤条件}, n) → 随机抽取至多 n 个文件（默认 %d，上限 %d），用于抽样了解大批文件

文件记录的字段（query_files/sample_files/get_file_info 返回的 map）：original_path, name, size, file_type, tagged, description?, new_path?, child_count?（带 ? 的字段为空时不出现；访问不存在的字段得到 undefined）
标准库模块需 import 后使用，如 json := import("json")

约定：
- 失败的内置函数返回 error 值，用 is_error(x) 判断，不会中断脚本（参数形状错误会直接报运行时错误）
- 脚本本身的编译/运行/超时错误会作为工具结果的 error 字段返回（不中断 agent 运行），读到后修正脚本重新调用即可
- 注释用 //（不支持 #），语句结束不用分号
- result 已预声明，赋值用 = 不要用 :=（result := ... 会报 'result' redeclared）
- print(...) 输出、脚本返回值分别限 %d 字节；脚本源码限 %d 字节；单次运行限 %d 秒，超时中止
- 可用标准库模块：%s（无 os/exec/env/文件/网络能力，一切数据访问必须经内置函数）`,
		scriptQueryDefault, scriptQueryMax,
		createCatNote,
		scriptSampleDefault, scriptSampleMax,
		maxResult, maxSize, int(timeout.Seconds()), scriptStdlibModules)
	runScript, err := utils.InferTool("run_script", desc, tb.runScript)
	if err != nil {
		return nil, fmt.Errorf("build run_script: %w", err)
	}
	return runScript, nil
}

func (tb *ToolBuilder) runScript(ctx context.Context, input *RunScriptInput) (*RunScriptOutput, error) {
	maxSize, timeout, maxResult := tb.scriptLimits()
	start := time.Now()

	// Model-fixable failures (bad input shape, compile/runtime errors,
	// script-side timeout) come back as the tool result's error field: the
	// agent reads the message and fixes the script on its next call, instead
	// of the whole run dying on a NodeRunError and burning a full retry. Only
	// context cancellation (user stop / run kill) stays a hard error.
	fail := func(format string, args ...any) (*RunScriptOutput, error) {
		err := fmt.Errorf(format, args...)
		return &RunScriptOutput{Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}, nil
	}

	if strings.TrimSpace(input.Script) == "" {
		return fail("script must not be empty")
	}
	if len(input.Script) > maxSize {
		return fail("script is %d bytes, exceeds the %d-byte limit", len(input.Script), maxSize)
	}

	out, err := tb.executeScript(ctx, input.Script, timeout, maxResult)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		// Failures are logged too: the entry records what the agent tried and
		// why it failed (bounded — the script source is capped by maxSize).
		return fail("%s", err.Error())
	}
	out.DurationMs = time.Since(start).Milliseconds()
	return out, nil
}

// executeScript compiles and runs one script under all guardrails.
func (tb *ToolBuilder) executeScript(ctx context.Context, src string, timeout time.Duration, maxResult int) (*RunScriptOutput, error) {
	s := tengo.NewScript([]byte(src))
	// Whitelisted pure-function stdlib only; file import stays disabled.
	s.SetImports(stdlib.GetModuleMap(strings.Split(scriptStdlibModules, ", ")...))
	s.SetMaxAllocs(scriptMaxAllocs)

	// print/println write into a per-run capped buffer, never to the process
	// stdout (the stdlib fmt module is deliberately absent).
	buf := &scriptPrintBuffer{cap: maxResult}
	if err := s.Add("print", &tengo.UserFunction{Name: "print", Value: buf.print}); err != nil {
		return nil, err
	}
	if err := s.Add("println", &tengo.UserFunction{Name: "println", Value: buf.print}); err != nil {
		return nil, err
	}

	// The return-value contract: the script assigns a global named `result`.
	// Pre-declare it as undefined — scripts must therefore assign with `=`
	// (`result := ...` redeclares the predeclared global and fails
	// compilation); an untouched (still undefined) result is skipped after
	// the run.
	if err := s.Add("result", tengo.UndefinedValue); err != nil {
		return nil, err
	}

	for name, fn := range tb.scriptBuiltins(ctx) {
		if err := s.Add(name, fn); err != nil {
			return nil, fmt.Errorf("register builtin %s: %w", name, err)
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	compiled, err := s.RunContext(runCtx)
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return nil, fmt.Errorf("script exceeded the %ds timeout and was aborted", int(timeout.Seconds()))
		}
		return nil, err
	}

	out := &RunScriptOutput{Output: buf.sb.String(), Truncated: buf.truncated}

	// Return-value contract: the script assigns a global named `result`.
	if compiled.IsDefined("result") {
		if v := compiled.Get("result").Value(); v != nil {
			raw, jerr := json.Marshal(v)
			if jerr != nil {
				return nil, fmt.Errorf("script result is not JSON-encodable: %w", jerr)
			}
			if len(raw) > maxResult {
				raw = raw[:maxResult]
				out.Truncated = true
			}
			out.Value = string(raw)
		}
	}
	return out, nil
}

// scriptPrintBuffer captures print output with a hard byte cap so a print
// loop cannot exhaust memory inside the timeout window. Scripts run on a
// single VM goroutine, so no locking is needed.
type scriptPrintBuffer struct {
	sb        strings.Builder
	cap       int
	truncated bool
}

func (b *scriptPrintBuffer) print(args ...tengo.Object) (tengo.Object, error) {
	parts := make([]string, len(args))
	for i, a := range args {
		if str, ok := a.(*tengo.String); ok {
			parts[i] = str.Value
		} else {
			parts[i] = a.String()
		}
	}
	b.write(strings.Join(parts, " ") + "\n")
	return tengo.UndefinedValue, nil
}

func (b *scriptPrintBuffer) write(s string) {
	if b.truncated {
		return
	}
	remaining := b.cap - b.sb.Len()
	if len(s) > remaining {
		b.sb.WriteString(s[:remaining])
		b.truncated = true
		return
	}
	b.sb.WriteString(s)
}

// ============ Script Builtins ============

// scriptError wraps a domain failure as a tengo error value so scripts can
// branch on is_error(x) instead of dying. Argument-shape mistakes still
// return Go errors, which surface as script runtime errors.
func scriptError(err error) tengo.Object {
	return &tengo.Error{Value: &tengo.String{Value: err.Error()}}
}

// scriptBuiltins assembles the category-domain builtin functions. Every one
// of them goes through the repository layer; the mutating ones reuse the tool
// core logic (setTargetCore/createCategoryCore) so validation and guard rails
// are identical to the standalone tools.
func (tb *ToolBuilder) scriptBuiltins(ctx context.Context) map[string]*tengo.UserFunction {
	builtins := map[string]*tengo.UserFunction{
		"query_files":     {Name: "query_files", Value: tb.scriptQueryFiles(ctx)},
		"count_files":     {Name: "count_files", Value: tb.scriptCountFiles(ctx)},
		"sample_files":    {Name: "sample_files", Value: tb.scriptSampleFiles(ctx)},
		"get_file_info":   {Name: "get_file_info", Value: tb.scriptGetFileInfo(ctx)},
		"get_categories":  {Name: "get_categories", Value: tb.scriptGetCategories(ctx)},
		"mark_tagged":     {Name: "mark_tagged", Value: tb.scriptMarkTagged(ctx)},
		"set_target":      {Name: "set_target", Value: tb.scriptSetTarget(ctx)},
		"set_category":    {Name: "set_category", Value: tb.scriptSetCategory(ctx)},
		"rename_category": {Name: "rename_category", Value: tb.scriptRenameCategory(ctx)},
	}
	// create_category follows the same session gate as the standalone tool:
	// without allow_auto_category the script must not bypass it.
	if tb.session.AllowAutoCategory {
		builtins["create_category"] = &tengo.UserFunction{Name: "create_category", Value: tb.scriptCreateCategory(ctx)}
	}
	return builtins
}

// --- argument helpers ---

func scriptArgCount(fn string, args []tengo.Object, min, max int) error {
	if len(args) < min || len(args) > max {
		if min == max {
			return fmt.Errorf("%s: expected %d argument(s), got %d", fn, min, len(args))
		}
		return fmt.Errorf("%s: expected %d-%d arguments, got %d", fn, min, max, len(args))
	}
	return nil
}

func scriptStringArg(fn, name string, o tengo.Object) (string, error) {
	s, ok := o.(*tengo.String)
	if !ok {
		return "", fmt.Errorf("%s: %s must be a string, got %s", fn, name, o.TypeName())
	}
	return s.Value, nil
}

func scriptIntArg(fn, name string, o tengo.Object) (int, error) {
	i, ok := o.(*tengo.Int)
	if !ok {
		return 0, fmt.Errorf("%s: %s must be an int, got %s", fn, name, o.TypeName())
	}
	return int(i.Value), nil
}

// scriptFilter mirrors the list_files filters available to scripts.
type scriptFilter struct {
	parentPath   *string
	fileType     string
	tagged       *bool
	categoryPath *string
	offset       int
	limit        int
}

// parseScriptFilter converts an optional trailing map argument into a
// scriptFilter. Unknown keys are rejected loudly so a typo never silently
// degrades a filter to "everything".
func parseScriptFilter(fn string, o tengo.Object) (*scriptFilter, error) {
	f := &scriptFilter{limit: scriptQueryDefault}
	if o == nil {
		return f, nil
	}
	m, ok := o.(*tengo.Map)
	if !ok {
		return nil, fmt.Errorf("%s: filter must be a map, got %s", fn, o.TypeName())
	}
	for key, val := range m.Value {
		switch key {
		case "parent_path":
			s, ok := val.(*tengo.String)
			if !ok {
				return nil, fmt.Errorf("%s: filter.parent_path must be a string", fn)
			}
			v := normalizePath(s.Value)
			f.parentPath = &v
		case "file_type":
			s, ok := val.(*tengo.String)
			if !ok {
				return nil, fmt.Errorf("%s: filter.file_type must be a string", fn)
			}
			f.fileType = s.Value
		case "tagged":
			b, ok := val.(*tengo.Bool)
			if !ok {
				return nil, fmt.Errorf("%s: filter.tagged must be a bool", fn)
			}
			v := b == tengo.TrueValue
			f.tagged = &v
		case "category_path":
			s, ok := val.(*tengo.String)
			if !ok {
				return nil, fmt.Errorf("%s: filter.category_path must be a string", fn)
			}
			f.categoryPath = &s.Value
		case "offset":
			i, ok := val.(*tengo.Int)
			if !ok {
				return nil, fmt.Errorf("%s: filter.offset must be an int", fn)
			}
			f.offset = int(i.Value)
		case "limit":
			i, ok := val.(*tengo.Int)
			if !ok {
				return nil, fmt.Errorf("%s: filter.limit must be an int", fn)
			}
			f.limit = int(i.Value)
		default:
			return nil, fmt.Errorf("%s: unknown filter key %q (want one of parent_path/file_type/tagged/category_path/offset/limit)", fn, key)
		}
	}
	if f.offset < 0 {
		f.offset = 0
	}
	if f.limit <= 0 {
		f.limit = scriptQueryDefault
	}
	if f.limit > scriptQueryMax {
		f.limit = scriptQueryMax
	}
	return f, nil
}

func (f *scriptFilter) toFileQuery(sessionID uint) db.FileQuery {
	q := db.FileQuery{
		SessionID:    sessionID,
		ParentPath:   f.parentPath,
		FileType:     f.fileType,
		Tagged:       f.tagged,
		CategoryPath: f.categoryPath,
	}
	// Convert offset+limit to page+pageSize, same as the list_files tool.
	if f.limit > 0 {
		q.Page = (f.offset / f.limit) + 1
		q.PageSize = f.limit
	}
	return q
}

// fileToScriptMap mirrors the list_files FileItem fields.
func fileToScriptMap(f db.FileEntry) map[string]interface{} {
	m := map[string]interface{}{
		"original_path": f.OriginalPath,
		"name":          f.Name,
		"size":          f.Size,
		"file_type":     f.FileType,
		"tagged":        f.Tagged,
	}
	if f.Description != "" {
		m["description"] = f.Description
	}
	if f.NewPath != "" {
		m["new_path"] = f.NewPath
	}
	if f.ChildCount != 0 {
		m["child_count"] = f.ChildCount
	}
	return m
}

func scriptObject(v interface{}) (tengo.Object, error) {
	obj, err := tengo.FromInterface(v)
	if err != nil {
		return nil, fmt.Errorf("result not convertible to script value: %w", err)
	}
	return obj, nil
}

// --- read builtins ---

func (tb *ToolBuilder) scriptQueryFiles(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("query_files", args, 0, 1); err != nil {
			return nil, err
		}
		var filterArg tengo.Object
		if len(args) == 1 {
			filterArg = args[0]
		}
		filter, err := parseScriptFilter("query_files", filterArg)
		if err != nil {
			return nil, err
		}
		q := filter.toFileQuery(tb.sessionID)
		isPull := tb.harnessShard(&q)
		files, total, err := tb.repo.ListFiles(q)
		if err != nil {
			return scriptError(err), nil
		}
		items := make([]interface{}, len(files))
		for i, f := range files {
			items[i] = fileToScriptMap(f)
		}
		if isPull && total > 0 {
			tb.harnessNotePull(fmt.Sprintf("%d files", len(files)), files)
			summaries := tb.archiveSummaries(ctx, files)
			for i, f := range files {
				if s := summaries[f.ID]; s != "" {
					if m, ok := items[i].(map[string]interface{}); ok {
						m["archive_summary"] = s
					}
				}
			}
		}
		return scriptObject(map[string]interface{}{"files": items, "total": total})
	}
}

func (tb *ToolBuilder) scriptCountFiles(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("count_files", args, 0, 1); err != nil {
			return nil, err
		}
		var filterArg tengo.Object
		if len(args) == 1 {
			filterArg = args[0]
		}
		filter, err := parseScriptFilter("count_files", filterArg)
		if err != nil {
			return nil, err
		}
		q := filter.toFileQuery(tb.sessionID)
		q.PageSize = 1 // only the count matters
		_, total, err := tb.repo.ListFiles(q)
		if err != nil {
			return scriptError(err), nil
		}
		return &tengo.Int{Value: total}, nil
	}
}

func (tb *ToolBuilder) scriptSampleFiles(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("sample_files", args, 0, 2); err != nil {
			return nil, err
		}
		n := scriptSampleDefault
		var filterArg tengo.Object
		// Forms: sample_files() / sample_files(n) / sample_files(filter) /
		// sample_files(filter, n).
		for _, a := range args {
			if iv, ok := a.(*tengo.Int); ok {
				n = int(iv.Value)
			} else {
				filterArg = a
			}
		}
		if n <= 0 {
			n = scriptSampleDefault
		}
		if n > scriptSampleMax {
			n = scriptSampleMax
		}
		filter, err := parseScriptFilter("sample_files", filterArg)
		if err != nil {
			return nil, err
		}
		files, err := tb.repo.SampleFiles(filter.toFileQuery(tb.sessionID), n)
		if err != nil {
			return scriptError(err), nil
		}
		items := make([]interface{}, len(files))
		for i, f := range files {
			items[i] = fileToScriptMap(f)
		}
		return scriptObject(items)
	}
}

func (tb *ToolBuilder) scriptGetFileInfo(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("get_file_info", args, 1, 1); err != nil {
			return nil, err
		}
		p, err := scriptStringArg("get_file_info", "path", args[0])
		if err != nil {
			return nil, err
		}
		f, err := tb.repo.GetFileByPath(tb.sessionID, normalizePath(p))
		if err != nil {
			return scriptError(fmt.Errorf("file not found: %s", p)), nil
		}
		m := fileToScriptMap(*f)
		m["parent_path"] = f.ParentPath
		return scriptObject(m)
	}
}

func (tb *ToolBuilder) scriptGetCategories(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("get_categories", args, 0, 0); err != nil {
			return nil, err
		}
		cats, err := tb.repo.ListCategories(tb.filesystemID)
		if err != nil {
			return scriptError(err), nil
		}
		items := make([]interface{}, len(cats))
		names := make([]string, len(cats))
		for i, c := range cats {
			names[i] = c.Name
			items[i] = map[string]interface{}{
				"name":           c.Name,
				"path":           c.Path,
				"structure":      c.Structure,
				"description":    c.Description,
				"agent_editable": c.AgentEditable,
			}
		}
		// The script is receiving the category list — record the snapshot for
		// the create_category version guard, exactly like list_categories does.
		tb.guard.note(names)
		return scriptObject(items)
	}
}

// --- mutating builtins (reuse the tool core logic) ---

func (tb *ToolBuilder) scriptMarkTagged(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("mark_tagged", args, 1, 1); err != nil {
			return nil, err
		}
		p, err := scriptStringArg("mark_tagged", "path", args[0])
		if err != nil {
			return nil, err
		}
		f, err := tb.repo.GetFileByPath(tb.sessionID, normalizePath(p))
		if err != nil {
			return scriptError(fmt.Errorf("file not found: %s", p)), nil
		}
		if f.Tagged {
			return tengo.TrueValue, nil
		}
		if err := tb.repo.MarkTaggedAtomic(tb.sessionID, normalizePath(p), tb.logger.GetBatch()); err != nil {
			return scriptError(err), nil
		}
		return tengo.TrueValue, nil
	}
}

func (tb *ToolBuilder) scriptSetTarget(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("set_target", args, 2, 2); err != nil {
			return nil, err
		}
		p, err := scriptStringArg("set_target", "path", args[0])
		if err != nil {
			return nil, err
		}
		np, err := scriptStringArg("set_target", "new_path", args[1])
		if err != nil {
			return nil, err
		}
		if _, err := tb.setTargetCore(ctx, p, np); err != nil {
			return scriptError(err), nil
		}
		return tengo.TrueValue, nil
	}
}

func (tb *ToolBuilder) scriptSetCategory(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("set_category", args, 2, 2); err != nil {
			return nil, err
		}
		p, err := scriptStringArg("set_category", "path", args[0])
		if err != nil {
			return nil, err
		}
		catName, err := scriptStringArg("set_category", "category_name", args[1])
		if err != nil {
			return nil, err
		}
		cat, err := tb.repo.GetCategoryByName(tb.filesystemID, catName)
		if err != nil {
			return scriptError(fmt.Errorf("category not found: %s", catName)), nil
		}
		f, err := tb.repo.GetFileByPath(tb.sessionID, normalizePath(p))
		if err != nil {
			return scriptError(fmt.Errorf("file not found: %s", p)), nil
		}
		target := strings.TrimSuffix(cat.Path, "/") + "/" + f.Name
		if _, err := tb.setTargetCore(ctx, p, target); err != nil {
			return scriptError(err), nil
		}
		return tengo.TrueValue, nil
	}
}

func (tb *ToolBuilder) scriptCreateCategory(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("create_category", args, 3, 3); err != nil {
			return nil, err
		}
		name, err := scriptStringArg("create_category", "name", args[0])
		if err != nil {
			return nil, err
		}
		p, err := scriptStringArg("create_category", "path", args[1])
		if err != nil {
			return nil, err
		}
		desc, err := scriptStringArg("create_category", "description", args[2])
		if err != nil {
			return nil, err
		}
		out, err := tb.createCategoryCore(ctx, &CreateCategoryInput{Name: name, Path: p, Description: desc})
		if err != nil {
			// Guard rejections and duplicate conflicts are domain failures —
			// hand them to the script as an inspectable error value.
			return scriptError(err), nil
		}
		// Round-trip through JSON to reuse the tool output shape verbatim
		// (including similar_categories/guidance).
		raw, _ := json.Marshal(out)
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("create_category: encode result: %w", err)
		}
		return scriptObject(m)
	}
}

func (tb *ToolBuilder) scriptRenameCategory(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("rename_category", args, 2, 2); err != nil {
			return nil, err
		}
		name, err := scriptStringArg("rename_category", "name", args[0])
		if err != nil {
			return nil, err
		}
		newName, err := scriptStringArg("rename_category", "new_name", args[1])
		if err != nil {
			return nil, err
		}
		newName = strings.TrimSpace(newName)
		if newName == "" {
			return nil, fmt.Errorf("rename_category: new_name must not be empty")
		}
		cat, err := tb.repo.GetCategoryByName(tb.filesystemID, name)
		if err != nil {
			return scriptError(fmt.Errorf("category not found: %s", name)), nil
		}
		if !cat.AgentEditable {
			return scriptError(fmt.Errorf("category '%s' is not editable by agent (user-managed)", name)), nil
		}
		if cat.Name == newName {
			return tengo.TrueValue, nil // idempotent no-op
		}
		cat.Name = newName
		if err := tb.repo.UpdateCategoryPath(cat, cat.Path); err != nil {
			return scriptError(fmt.Errorf("rename category: %w", err)), nil
		}
		// The rename changed the name set the agent knows about; refresh the
		// create_category guard snapshot so a later create in the same script
		// is judged against the post-rename world.
		if cats, err := tb.repo.ListCategories(tb.filesystemID); err == nil {
			names := make([]string, len(cats))
			for i, c := range cats {
				names[i] = c.Name
			}
			tb.guard.note(names)
		}
		return tengo.TrueValue, nil
	}
}

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"FileEngine/internal/db"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/stdlib"
)

// Security: the stdlib is trimmed to pure-function modules; os, fmt (writes
// to process stdout) and file import are unavailable. times is excluded
// because its sleep() blocks inside a VM builtin, where the RunContext abort
// flag is never checked, so it would bypass script_timeout. All data access
// goes through the builtins, which reuse the tool layer's guard rails.

type RunScriptInput struct {
	Script string `json:"script" jsonschema_description:"Tengo 脚本源码。用 print(...) 打印输出；给全局变量 result 赋值作为返回值。可用内置函数见工具描述"`
}

type RunScriptOutput struct {
	Output     string `json:"output"`
	Value      string `json:"value,omitempty"`     // JSON of the script's `result` global, when set
	Truncated  bool   `json:"truncated,omitempty"` // output or value hit script_max_result_size
	Error      string `json:"error,omitempty"`     // script compile/runtime/timeout failure, fed back for the model to fix
	DurationMs int64  `json:"duration_ms"`
}

const (
	// scriptMaxAllocs counts allocations, not bytes: an exponential-growth loop
	// (x += x) can still OOM the process inside the timeout window, and Go's
	// out-of-memory is fatal. Accepted: tengo has no byte-level memory cap.
	scriptMaxAllocs     = 4_000_000
	scriptQueryDefault  = 200  // default query_files limit
	scriptQueryMax      = 1000 // hard cap on query_files limit
	scriptSampleDefault = 10   // default sample_files count
	scriptSampleMax     = 200  // hard cap on sample_files count
	scriptStdlibModules = "text, math, json, base64, hex, rand, enum"
)

// Same fallbacks as config.setDefaults: tests build AgentConfig literals that
// never pass through it.
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
		maxResult = 8 * 1024
	}
	return maxSize, time.Duration(secs) * time.Second, maxResult
}

func (tb *ToolBuilder) buildScriptTool() (tool.BaseTool, error) {
	if !tb.session.AllowScript {
		return nil, nil
	}
	maxSize, timeout, maxResult := tb.scriptLimits()
	createCatNote := map[bool]string{true: "", false: "（本会话未开启 allow_auto_category，不可用）"}[tb.session.AllowAutoCategory] + categoryPolicySuffix(tb.cfg.CategoryPolicy)
	desc := fmt.Sprintf(`运行 Tengo 脚本（类 Go 语法）做批量查询和批量决定，代替逐条工具调用。返回 print 输出和全局变量 result 的 JSON。

内置函数：
- query_files({parent_path?, file_type?, undecided?, category_path?, cursor?, limit?}) → {files, has_more, next_cursor}（limit 默认 %d，上限 %d；翻页时把 next_cursor 作为 cursor 传回）；count_files({parent_path?, file_type?, undecided?, category_path?}) → 整数；sample_files([过滤,] n) → 随机 n 个（上限 %d）
- list_dirs(parent?) → 子目录数组；signals(path) → 目录信号（标记、扩展名、版本组、规模）
- get_file_info(path)；get_categories()
- set_targets([{path, new_path, reason, confidence?}]) → {ok, failed, renamed}；set_target(path, new_path) → 实际目标路径
- set_category(path, 分类名, relative_to?) → 实际目标路径（分类路径 + 相对路径，保留子结构；无 relative_to 时取当前单元根目录下的相对路径，否则只取文件名）
- decide_dir(path, role, new_path?, reason?) → {role, new_path}；role 为 unit / container / skip
- apply_rule(规则 id 或名称) → 本次匹配数
- create_category(name, path, description)；rename_category(name, new_name)%s

文件记录字段：original_path, name, size, file_type, role, reason?, new_path?, description?, child_count?
约定：失败的内置函数返回 error 值（is_error(x) 判断）；result 已预声明，用 = 赋值；注释用 //；输出和返回值各限 %d 字节，源码限 %d 字节，单次 %d 秒；可 import 的模块：%s`,
		scriptQueryDefault, scriptQueryMax, scriptSampleMax,
		createCatNote,
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

	// Model-fixable failures come back in the result's error field so the agent
	// fixes the script on its next call instead of the run dying; only context
	// cancellation stays a hard error.
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
		return fail("%s", err.Error())
	}
	out.DurationMs = time.Since(start).Milliseconds()
	return out, nil
}

func (tb *ToolBuilder) executeScript(ctx context.Context, src string, timeout time.Duration, maxResult int) (*RunScriptOutput, error) {
	s := tengo.NewScript([]byte(src))
	s.SetImports(stdlib.GetModuleMap(strings.Split(scriptStdlibModules, ", ")...))
	s.SetMaxAllocs(scriptMaxAllocs)

	// Never the process stdout: the stdlib fmt module is deliberately absent.
	buf := &scriptPrintBuffer{cap: maxResult}
	if err := s.Add("print", &tengo.UserFunction{Name: "print", Value: buf.print}); err != nil {
		return nil, err
	}
	if err := s.Add("println", &tengo.UserFunction{Name: "println", Value: buf.print}); err != nil {
		return nil, err
	}

	// Predeclared, so scripts must assign with `=`: `result := ...` redeclares
	// the global and fails compilation.
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

// Capped so a print loop cannot exhaust memory inside the timeout window.
// Scripts run on a single VM goroutine, so no locking is needed.
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

// Domain failures are tengo error values so scripts can branch on
// is_error(x); argument-shape mistakes stay Go errors.
func scriptError(err error) tengo.Object {
	return &tengo.Error{Value: &tengo.String{Value: err.Error()}}
}

// The mutating builtins reuse the tool cores, so validation and guard rails
// are identical to the standalone tools.
func (tb *ToolBuilder) scriptBuiltins(ctx context.Context) map[string]*tengo.UserFunction {
	builtins := map[string]*tengo.UserFunction{
		"query_files":     {Name: "query_files", Value: tb.scriptQueryFiles(ctx)},
		"count_files":     {Name: "count_files", Value: tb.scriptCountFiles(ctx)},
		"sample_files":    {Name: "sample_files", Value: tb.scriptSampleFiles(ctx)},
		"get_file_info":   {Name: "get_file_info", Value: tb.scriptGetFileInfo(ctx)},
		"get_categories":  {Name: "get_categories", Value: tb.scriptGetCategories(ctx)},
		"set_targets":     {Name: "set_targets", Value: tb.scriptSetTargets(ctx)},
		"decide_dir":      {Name: "decide_dir", Value: tb.scriptDecideDir(ctx)},
		"apply_rule":      {Name: "apply_rule", Value: tb.scriptApplyRule(ctx)},
		"signals":         {Name: "signals", Value: tb.scriptSignals(ctx)},
		"list_dirs":       {Name: "list_dirs", Value: tb.scriptListDirs(ctx)},
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

type scriptFilter struct {
	parentPath   *string
	fileType     string
	undecided    bool
	categoryPath *string
	cursor       uint
	limit        int
}

// Unknown keys are rejected so a typo never silently widens a filter to
// everything.
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
		case "undecided":
			b, ok := val.(*tengo.Bool)
			if !ok {
				return nil, fmt.Errorf("%s: filter.undecided must be a bool", fn)
			}
			f.undecided = b == tengo.TrueValue
		case "category_path":
			s, ok := val.(*tengo.String)
			if !ok {
				return nil, fmt.Errorf("%s: filter.category_path must be a string", fn)
			}
			f.categoryPath = &s.Value
		case "cursor":
			i, ok := val.(*tengo.Int)
			if !ok || i.Value < 0 {
				return nil, fmt.Errorf("%s: filter.cursor must be a non-negative int", fn)
			}
			f.cursor = uint(i.Value)
		case "limit":
			i, ok := val.(*tengo.Int)
			if !ok {
				return nil, fmt.Errorf("%s: filter.limit must be an int", fn)
			}
			f.limit = int(i.Value)
		default:
			return nil, fmt.Errorf("%s: unknown filter key %q (want one of parent_path/file_type/undecided/category_path/cursor/limit)", fn, key)
		}
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
		CategoryPath: f.categoryPath,
	}
	if f.undecided {
		role := db.RoleUndecided
		q.Role = &role
	}
	return q
}

func fileToScriptMap(f db.FileEntry) map[string]interface{} {
	m := map[string]interface{}{
		"original_path": f.OriginalPath,
		"name":          SanitizeUntrustedName(f.Name),
		"role":          f.Role,
		"size":          f.Size,
		"file_type":     f.FileType,
		"tagged":        f.Tagged,
	}
	if f.Description != "" {
		m["description"] = f.Description
	}
	if f.Reason != "" {
		m["reason"] = f.Reason
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
		files, more, err := tb.repo.ListFilesAfter(filter.toFileQuery(tb.sessionID), filter.cursor, filter.limit)
		if errors.Is(err, db.ErrBadCursor) {
			return scriptError(fmt.Errorf("cursor 已失效，请不带 cursor 从头查询")), nil
		} else if err != nil {
			return scriptError(err), nil
		}
		items := make([]interface{}, len(files))
		for i, f := range files {
			items[i] = fileToScriptMap(f)
		}
		out := map[string]interface{}{"files": items, "has_more": more}
		if more {
			out["next_cursor"] = int64(files[len(files)-1].ID)
		}
		return scriptObject(out)
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
		q.PageSize = 1
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
		// Record the snapshot for the create_category version guard, like
		// list_categories.
		tb.guard.note(names)
		return scriptObject(items)
	}
}

func (tb *ToolBuilder) scriptSetTarget(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("set_target", args, 2, 3); err != nil {
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
		reason := "脚本"
		if len(args) == 3 {
			if reason, err = scriptStringArg("set_target", "reason", args[2]); err != nil {
				return nil, err
			}
		}
		role := db.RoleFile
		if np == "" {
			role = db.RoleUndecided
		}
		res, err := tb.decideOne(db.Decision{Path: p, Role: role, NewPath: np, Reason: reason})
		if err != nil {
			return scriptError(err), nil
		}
		if res.NewPath == "" {
			return tengo.TrueValue, nil
		}
		return &tengo.String{Value: res.NewPath}, nil
	}
}

func (tb *ToolBuilder) scriptSetCategory(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("set_category", args, 2, 3); err != nil {
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
		relativeTo := ""
		if len(args) == 3 {
			if relativeTo, err = scriptStringArg("set_category", "relative_to", args[2]); err != nil {
				return nil, err
			}
		}
		cat, err := tb.repo.GetCategoryByName(tb.filesystemID, catName)
		if err != nil {
			return scriptError(fmt.Errorf("category not found: %s", catName)), nil
		}
		f, err := tb.repo.GetFileByPath(tb.sessionID, normalizePath(p))
		if err != nil {
			return scriptError(fmt.Errorf("file not found: %s", p)), nil
		}
		rel, err := categoryRelativePath(*f, relativeTo, tb.unitRoot)
		if err != nil {
			return scriptError(err), nil
		}
		target := strings.TrimSuffix(cat.Path, "/") + "/" + rel
		res, err := tb.decideOne(db.Decision{Path: p, Role: db.RoleFile, NewPath: target, Reason: "归入「" + cat.Name + "」"})
		if err != nil {
			return scriptError(err), nil
		}
		return &tengo.String{Value: res.NewPath}, nil
	}
}

// categoryRelativePath is the part of a file's path that set_category keeps
// below the category path, so sub-structure survives instead of flattening:
//   - relative_to given: the path below that directory (the file must lie
//     under it; "" / "." means the scan root, i.e. the full original path)
//   - otherwise the path below the current work unit's root (SetUnitRoot) when
//     the file lies under it
//   - otherwise just the file name (direct placement in the category)
func categoryRelativePath(f db.FileEntry, relativeTo, unitRoot string) (string, error) {
	if relativeTo != "" {
		base := strings.Trim(normalizePath(relativeTo), "/")
		if base == "" || base == "." {
			return f.OriginalPath, nil
		}
		rel, ok := strings.CutPrefix(f.OriginalPath, base+"/")
		if !ok {
			return "", fmt.Errorf("set_category: %s is not under relative_to %s", f.OriginalPath, relativeTo)
		}
		return rel, nil
	}
	if unitRoot != "" {
		if rel, ok := strings.CutPrefix(f.OriginalPath, unitRoot+"/"); ok {
			return rel, nil
		}
	}
	return f.Name, nil
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
			return scriptError(err), nil
		}
		// Round-trip through JSON to reuse the tool output shape verbatim.
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
		// The rename changed the name set; refresh the guard snapshot so a later
		// create in the same script sees the post-rename set.
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

func (tb *ToolBuilder) scriptSetTargets(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("set_targets", args, 1, 1); err != nil {
			return nil, err
		}
		arr, ok := args[0].(*tengo.Array)
		if !ok {
			return nil, fmt.Errorf("set_targets: argument must be an array of maps")
		}
		ds := make([]db.Decision, 0, len(arr.Value))
		for i, o := range arr.Value {
			m, ok := o.(*tengo.Map)
			if !ok {
				return nil, fmt.Errorf("set_targets: item %d must be a map", i)
			}
			d := db.Decision{Role: db.RoleFile, Reason: "脚本"}
			for k, v := range m.Value {
				switch k {
				case "path", "new_path", "reason":
					sv, ok := v.(*tengo.String)
					if !ok {
						return nil, fmt.Errorf("set_targets: item %d %s must be a string", i, k)
					}
					switch k {
					case "path":
						d.Path = sv.Value
					case "new_path":
						d.NewPath = sv.Value
					default:
						d.Reason = sv.Value
					}
				case "confidence":
					switch n := v.(type) {
					case *tengo.Float:
						d.Confidence = n.Value
					case *tengo.Int:
						d.Confidence = float64(n.Value)
					default:
						return nil, fmt.Errorf("set_targets: item %d confidence must be a number", i)
					}
				default:
					return nil, fmt.Errorf("set_targets: item %d unknown key %q", i, k)
				}
			}
			if d.NewPath == "" {
				d.Role = db.RoleUndecided
			}
			ds = append(ds, d)
		}
		out, _, err := tb.decide(ds)
		if err != nil {
			return scriptError(err), nil
		}
		if out.Error != "" {
			return scriptError(fmt.Errorf("%s", out.Error)), nil
		}
		raw, _ := json.Marshal(out)
		var m map[string]interface{}
		_ = json.Unmarshal(raw, &m)
		return scriptObject(m)
	}
}

func (tb *ToolBuilder) scriptDecideDir(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("decide_dir", args, 2, 4); err != nil {
			return nil, err
		}
		strs := make([]string, len(args))
		for i, a := range args {
			v, err := scriptStringArg("decide_dir", "argument", a)
			if err != nil {
				return nil, err
			}
			strs[i] = v
		}
		d := db.Decision{Path: strs[0], Role: strs[1], Reason: "脚本"}
		if len(strs) > 2 {
			d.NewPath = strs[2]
		}
		if len(strs) > 3 && strs[3] != "" {
			d.Reason = strs[3]
		}
		res, err := tb.decideOne(d)
		if err != nil {
			return scriptError(err), nil
		}
		return scriptObject(map[string]interface{}{"role": res.Role, "new_path": res.NewPath})
	}
}

func (tb *ToolBuilder) scriptApplyRule(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("apply_rule", args, 1, 1); err != nil {
			return nil, err
		}
		rules, err := tb.repo.ListRules(tb.sessionID)
		if err != nil {
			return scriptError(err), nil
		}
		var id uint
		for _, r := range rules {
			switch a := args[0].(type) {
			case *tengo.Int:
				if uint(a.Value) == r.ID {
					id = r.ID
				}
			case *tengo.String:
				if a.Value == r.Name {
					id = r.ID
				}
			default:
				return nil, fmt.Errorf("apply_rule: argument must be a rule id or name")
			}
		}
		if id == 0 {
			return scriptError(fmt.Errorf("规则不存在：%s", args[0].String())), nil
		}
		stats, err := tb.repo.ApplyRules(tb.sessionID, id, false)
		if err != nil {
			return scriptError(err), nil
		}
		n := 0
		for _, st := range stats {
			n += st.Matched
		}
		return &tengo.Int{Value: int64(n)}, nil
	}
}

func (tb *ToolBuilder) scriptSignals(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("signals", args, 1, 1); err != nil {
			return nil, err
		}
		p, err := scriptStringArg("signals", "path", args[0])
		if err != nil {
			return nil, err
		}
		sig, err := tb.dirSignals(strings.Trim(normalizePath(p), "/"))
		if err != nil {
			return scriptError(err), nil
		}
		raw, _ := json.Marshal(sig)
		var m map[string]interface{}
		_ = json.Unmarshal(raw, &m)
		return scriptObject(m)
	}
}

func (tb *ToolBuilder) scriptListDirs(ctx context.Context) tengo.CallableFunc {
	return func(args ...tengo.Object) (tengo.Object, error) {
		if err := scriptArgCount("list_dirs", args, 0, 1); err != nil {
			return nil, err
		}
		parent := ""
		if len(args) == 1 {
			p, err := scriptStringArg("list_dirs", "parent", args[0])
			if err != nil {
				return nil, err
			}
			parent = strings.Trim(normalizePath(p), "/")
		}
		dirs, _, err := tb.repo.ListFiles(db.FileQuery{SessionID: tb.sessionID, ParentPath: &parent, FileType: "directory", Page: 1, PageSize: scriptQueryMax})
		if err != nil {
			return scriptError(err), nil
		}
		items := make([]interface{}, len(dirs))
		for i, d := range dirs {
			items[i] = fileToScriptMap(d)
		}
		return scriptObject(items)
	}
}

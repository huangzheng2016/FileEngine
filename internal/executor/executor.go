// Package executor carries out a session's decided plan on the filesystem
// (phase 2). Safety rules: a source is only ever deleted after its copy was
// verified; existing targets are never overwritten (conflicts are skipped and
// reported); directories are moved by an atomic rename or file by file, and a
// source directory is removed only once it is empty. Every step is journaled
// as a db.ExecOp before it happens, which makes runs resumable after a crash
// and revertible afterwards.
package executor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"strings"
	"sync"
	"time"

	"FileEngine/internal/db"
	"FileEngine/internal/pathutil"
	"FileEngine/internal/remotefs"
)

const (
	ModeCopy = "copy"
	ModeMove = "move"
)

const DefaultHashLimit int64 = 64 << 20

type Options struct {
	Mode         string `json:"mode"`                    // copy | move
	Sample       int    `json:"sample,omitempty"`        // execute only the first N runnable entries
	CategoryPath string `json:"category_path,omitempty"` // execute only entries targeting this category path
}

const (
	IssueInvalidTarget = "invalid_target" // target path illegal or empty
	IssueIntoSelf      = "into_self"      // target equals or lies inside the source
	IssueNested        = "nested"         // an ancestor directory carries its own decision
	IssueCollision     = "collision"      // another entry targets the same path (case/NFC-insensitive)
	IssueTargetExists  = "target_exists"  // something already exists at the target
	IssueSourceChanged = "source_changed" // source size/mtime differ from the scan
	IssueSourceMissing = "source_missing" // source no longer exists
	IssueFailed        = "failed"         // the filesystem operation failed
)

type Issue struct {
	EntryID      uint   `json:"entry_id"`
	OriginalPath string `json:"original_path"`
	NewPath      string `json:"new_path"`
	Code         string `json:"code"`
	Detail       string `json:"detail"`
}

type Result struct {
	Total   int      `json:"total"`
	Success int      `json:"success"`
	Failed  int      `json:"failed"`
	Skipped int      `json:"skipped"`
	Issues  []Issue  `json:"issues"`
	Notes   []string `json:"notes,omitempty"`
}

func (r *Result) issue(f *db.FileEntry, code, detail string) {
	r.Issues = append(r.Issues, Issue{EntryID: f.ID, OriginalPath: f.OriginalPath, NewPath: f.NewPath, Code: code, Detail: detail})
}

type skipError struct{ code, detail string }

func (e *skipError) Error() string { return e.detail }

func skip(code, detail string) error { return &skipError{code: code, detail: detail} }

type Executor struct {
	repo            *db.Repository
	fs              remotefs.RemoteFS
	HashLimit       int64
	ListConcurrency int

	sessionID    uint
	run          *db.AgentRun
	dirs         map[string]bool // directories known to exist in this run
	renameBroken bool            // in-place rename failed once (cross-device); go straight to copy
	lastEmit     time.Time
}

func New(repo *db.Repository, fs remotefs.RemoteFS) *Executor {
	return &Executor{repo: repo, fs: fs, HashLimit: DefaultHashLimit, ListConcurrency: 8}
}

func cleanPath(p string) string {
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}

func parentOf(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

func validTarget(p string) error {
	if strings.TrimSpace(p) == "" {
		return errors.New("目标位置为空")
	}
	if err := pathutil.ValidateFSPath(p); err != nil {
		return fmt.Errorf("目标位置无效：%w", err)
	}
	return nil
}

// The run owns fs and closes it when it ends.
func Start(repo *db.Repository, fs remotefs.RemoteFS, sessionID uint, opts Options) (*db.AgentRun, error) {
	if opts.Mode != ModeCopy && opts.Mode != ModeMove {
		return nil, fmt.Errorf("执行模式只能是 %s 或 %s", ModeCopy, ModeMove)
	}
	title := "执行"
	switch {
	case opts.CategoryPath != "":
		title += "（" + opts.CategoryPath + "）"
	case opts.Sample > 0:
		title += fmt.Sprintf("（前 %d 项）", opts.Sample)
	}
	run := &db.AgentRun{SessionID: sessionID, Phase: db.RunPhaseExecuting, Title: title, ExecMode: opts.Mode}
	if err := repo.InsertRun(run); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	db.RegisterRunCancel(run.ID, cancel)
	go func() {
		defer fs.Close()
		defer cancel()
		e := New(repo, fs)
		res, err := e.Execute(ctx, run, opts)
		e.finish(run, res, err, "执行")
	}()
	return run, nil
}

var ErrNotExecuting = errors.New("只能撤销执行")

func StartRevert(repo *db.Repository, fs remotefs.RemoteFS, runID uint) (*db.AgentRun, error) {
	var target db.AgentRun
	if err := repo.DB().First(&target, runID).Error; err != nil {
		return nil, fmt.Errorf("读取运行 %d：%w", runID, err)
	}
	if target.Phase == db.RunPhaseRevert {
		return nil, db.ErrRevertOfRevert
	}
	if target.Phase != db.RunPhaseExecuting {
		return nil, ErrNotExecuting
	}
	run := &db.AgentRun{SessionID: target.SessionID, Phase: db.RunPhaseRevert, Title: "撤销", RevertsRunID: &target.ID}
	if err := repo.InsertRun(run); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	db.RegisterRunCancel(run.ID, cancel)
	go func() {
		defer fs.Close()
		defer cancel()
		e := New(repo, fs)
		res, err := e.Revert(ctx, run, runID)
		e.finish(run, res, err, "撤销")
	}()
	return run, nil
}

func (e *Executor) finish(run *db.AgentRun, res *Result, err error, verb string) {
	if res == nil {
		res = &Result{}
	}
	e.emit(res, true)
	status, errMsg := db.RunDone, ""
	switch {
	case errors.Is(err, context.Canceled):
		status = db.RunStopped
	case err != nil:
		status, errMsg = db.RunError, err.Error()
		log.Printf("executor run %d: %v", run.ID, err)
	case res.Failed > 0 || res.Skipped > 0:
		status = db.RunPartial
	}
	if rerr := e.repo.RefreshSessionCounts(run.SessionID); rerr != nil {
		log.Printf("executor run %d: refresh counts: %v", run.ID, rerr)
	}
	if ferr := e.repo.FinishRun(run, status, Summary(verb, res), errMsg, db.TokenCounts{}); ferr != nil {
		log.Printf("executor run %d: finish: %v", run.ID, ferr)
	}
}

func (e *Executor) emit(res *Result, force bool) {
	if e.run == nil {
		return
	}
	if !force && time.Since(e.lastEmit) < 250*time.Millisecond {
		return
	}
	e.lastEmit = time.Now()
	db.Emit(e.run.SessionID, e.run.ID, db.EvProgress, db.ProgressData{
		RunID: e.run.ID, Phase: e.run.Phase,
		Total: res.Total, Success: res.Success, Failed: res.Failed, Skipped: res.Skipped,
	})
}

var issueLabels = map[string]string{
	IssueInvalidTarget: "目标位置无效",
	IssueIntoSelf:      "目标位于源目录内",
	IssueNested:        "上级目录已有决定",
	IssueCollision:     "目标位置冲突",
	IssueTargetExists:  "目标已存在",
	IssueSourceChanged: "源文件已变化",
	IssueSourceMissing: "源文件不存在",
	IssueFailed:        "操作失败",
}

func Summary(verb string, res *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s结果\n\n共 %d 项：成功 %d，失败 %d，跳过 %d。\n", verb, res.Total, res.Success, res.Failed, res.Skipped)
	if len(res.Issues) > 0 {
		b.WriteString("\n## 问题\n\n")
		for i, is := range res.Issues {
			if i == 200 {
				fmt.Fprintf(&b, "- … 另有 %d 项\n", len(res.Issues)-200)
				break
			}
			fmt.Fprintf(&b, "- **%s** `%s` → `%s`：%s\n", issueLabels[is.Code], is.OriginalPath, is.NewPath, is.Detail)
		}
	}
	if len(res.Notes) > 0 {
		b.WriteString("\n## 备注\n\n")
		for i, n := range res.Notes {
			if i == 50 {
				fmt.Fprintf(&b, "- … 另有 %d 条\n", len(res.Notes)-50)
				break
			}
			b.WriteString("- " + n + "\n")
		}
	}
	return b.String()
}

// Canceling ctx stops before the next step; it never triggers a delete.
func (e *Executor) Execute(ctx context.Context, run *db.AgentRun, opts Options) (*Result, error) {
	if opts.Mode != ModeCopy && opts.Mode != ModeMove {
		return nil, fmt.Errorf("执行模式只能是 %s 或 %s", ModeCopy, ModeMove)
	}
	e.run, e.sessionID, e.dirs = run, run.SessionID, map[string]bool{}
	res := &Result{}
	if err := e.Recover(ctx, run.SessionID); err != nil {
		return res, fmt.Errorf("恢复中断的执行失败：%w", err)
	}

	entries, err := e.plan(ctx, opts, res)
	if err != nil {
		return res, err
	}
	res.Total = len(entries) + res.Skipped
	e.emit(res, true)

	for i := range entries {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		f := &entries[i]
		err := e.executeEntry(ctx, f, opts.Mode)
		var se *skipError
		switch {
		case err == nil:
			res.Success++
			if merr := e.repo.MarkEntryExecuted(f.ID, opts.Mode); merr != nil {
				return res, merr
			}
		case errors.As(err, &se):
			res.Skipped++
			res.issue(f, se.code, se.detail)
		case errors.Is(err, context.Canceled):
			res.Failed++
			res.issue(f, IssueFailed, "已停止，未完成的部分保留在原处")
			return res, err
		default:
			res.Failed++
			res.issue(f, IssueFailed, err.Error())
			log.Printf("execute %s %s -> %s: %v", opts.Mode, f.OriginalPath, f.NewPath, err)
		}
		e.emit(res, false)
	}
	return res, nil
}

func (e *Executor) executeEntry(ctx context.Context, f *db.FileEntry, mode string) error {
	// Re-validated right before the operation: rows may change after planning.
	if err := validTarget(f.NewPath); err != nil {
		return skip(IssueInvalidTarget, err.Error())
	}
	if _, err := pathutil.RootRelative(f.OriginalPath); err != nil {
		return skip(IssueFailed, "源路径非法："+err.Error())
	}
	src, dst := cleanPath(f.OriginalPath), cleanPath(f.NewPath)
	st, err := e.fs.Stat(ctx, src)
	if err != nil {
		return skip(IssueSourceMissing, "源文件不存在或无法访问："+err.Error())
	}
	if changed := staleReason(f, st); changed != "" {
		return skip(IssueSourceChanged, changed)
	}
	resume := false
	if _, err := e.fs.Stat(ctx, dst); err == nil {
		if !(f.FileType == "directory" && e.owned(dst)) {
			return skip(IssueTargetExists, "目标位置已有同名文件或目录")
		}
		resume = true // our own partially executed directory
	}
	if err := e.ensureDir(ctx, f.ID, parentOf(dst)); err != nil {
		return err
	}
	switch f.FileType {
	case "directory":
		return e.transferDir(ctx, f.ID, src, dst, mode, resume)
	case "symlink":
		if mode == ModeCopy {
			return skip(IssueFailed, "符号链接不复制")
		}
		return e.renameOnly(ctx, f.ID, src, dst, st)
	default:
		return e.transferFile(ctx, f.ID, src, dst, st, mode)
	}
}

func staleReason(f *db.FileEntry, st *remotefs.FileInfo) string {
	isDir := f.FileType == "directory"
	if st.IsDir != isDir {
		return "源的类型已变化"
	}
	if isDir || f.FileType == "symlink" {
		return ""
	}
	if st.Size != f.Size {
		return fmt.Sprintf("源文件大小已变化（扫描时 %d，现在 %d）", f.Size, st.Size)
	}
	if !f.ModTime.IsZero() && !st.ModTime.IsZero() && f.ModTime.Unix() != st.ModTime.Unix() {
		return "源文件修改时间已变化"
	}
	return ""
}

// A directory a previous, interrupted run already started filling.
func (e *Executor) owned(dst string) bool {
	op, err := e.repo.FindDoneExecOp(e.sessionID, dst)
	return err == nil && op != nil
}

func (e *Executor) listConcurrent(ctx context.Context, dirs []string) map[string][]remotefs.FileInfo {
	out := make(map[string][]remotefs.FileInfo, len(dirs))
	var mu sync.Mutex
	n := e.ListConcurrency
	if n < 1 {
		n = 1
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range jobs {
				list, err := e.fs.List(ctx, d)
				if err != nil {
					continue
				}
				mu.Lock()
				out[d] = list
				mu.Unlock()
			}
		}()
	}
	for _, d := range dirs {
		if ctx.Err() != nil {
			break
		}
		jobs <- d
	}
	close(jobs)
	wg.Wait()
	return out
}

package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// Finished steps are completed (a verified move deletes its unchanged
// source); unfinished ones are rolled back (a partial copy is removed).
func (e *Executor) Recover(ctx context.Context, sessionID uint) error {
	e.sessionID = sessionID
	ops, err := e.repo.ListUnfinishedExecOps(sessionID)
	if err != nil {
		return err
	}
	for i := range ops {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.recoverOp(ctx, &ops[i]); err != nil {
			return fmt.Errorf("op %d (%s %s -> %s): %w", ops[i].ID, ops[i].Kind, ops[i].Src, ops[i].Dst, err)
		}
	}
	return e.reconcileFinished()
}

func (e *Executor) reconcileFinished() error {
	done, err := e.repo.ListDoneOpsOfPlannedEntries(e.sessionID)
	if err != nil {
		return err
	}
	for i := range done {
		mode := ModeMove
		if done[i].Kind == db.ExecKindCopy {
			mode = ModeCopy
		}
		if err := e.markIfEntryOp(&done[i], mode); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) recoverOp(ctx context.Context, op *model.ExecOp) error {
	const interrupted = "中断"
	switch op.Kind {
	case db.ExecKindRename:
		landed, err := e.renameLanded(ctx, op)
		if err != nil {
			return e.unsettled(ctx, op, err)
		}
		if landed {
			ds, err := e.fs.Stat(ctx, op.Dst)
			if err != nil {
				return e.unsettled(ctx, op, err)
			}
			if err := e.repo.SetExecOpStateDst(op, db.ExecDone, "", ds.Size, ds.ModTime); err != nil {
				return err
			}
			return e.markIfEntryOp(op, ModeMove)
		}
		return e.setState(op, db.ExecFailed, interrupted)
	case db.ExecKindMkdir:
		st, ok, err := e.stat(ctx, op.Dst)
		if err != nil {
			return e.unsettled(ctx, op, err)
		}
		if ok && st.IsDir {
			return e.setState(op, db.ExecDone, "")
		}
		return e.setState(op, db.ExecFailed, interrupted)
	case db.ExecKindRmdir:
		_, ok, err := e.stat(ctx, op.Src)
		if err != nil {
			return e.unsettled(ctx, op, err)
		}
		if !ok {
			return e.setState(op, db.ExecDone, "")
		}
		return e.setState(op, db.ExecFailed, interrupted)
	case db.ExecKindCopy, db.ExecKindMove:
		if op.State == db.ExecPending {
			_, ok, err := e.stat(ctx, op.Dst)
			if err != nil {
				return e.unsettled(ctx, op, err)
			}
			if !ok {
				return e.setState(op, db.ExecFailed, interrupted)
			}
			if !op.DstCreated {
				// Nothing proves the file at dst came from this op.
				msg := "目标位置已有无法确认来源的文件，未删除也未覆盖"
				e.failOp(op, msg)
				e.notes = append(e.notes, "「"+op.Dst+"」"+msg)
				return nil
			}
		}
		if err := e.finishCopy(ctx, op); err != nil {
			var se *skipError
			var de *unfinishedError
			switch {
			case errors.As(err, &de):
				return e.unsettled(ctx, op, de.Unwrap())
			case errors.As(err, &se) || op.State == db.ExecFailed:
				return nil
			}
			return err
		}
		mode := ModeCopy
		if op.Kind == db.ExecKindMove {
			mode = ModeMove
		}
		return e.markIfEntryOp(op, mode)
	}
	return e.setState(op, db.ExecFailed, interrupted)
}

// unsettled leaves an op in its journaled state because the filesystem could
// not be read; it is retried by the next Recover and never blocks other work.
func (e *Executor) unsettled(ctx context.Context, op *model.ExecOp, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	e.notes = append(e.notes, fmt.Sprintf("「%s」的执行记录暂时无法确认，稍后重试：%v", op.Src+" → "+op.Dst, err))
	return nil
}

func (e *Executor) markIfEntryOp(op *model.ExecOp, mode string) error {
	if op.EntryID == 0 {
		return nil
	}
	f, err := e.repo.GetFile(op.EntryID)
	if err != nil {
		return nil
	}
	if cleanPath(f.OriginalPath) == op.Src {
		return e.repo.MarkEntryExecuted(f.ID, mode)
	}
	return nil
}

func dstUnchanged(st *remotefs.FileInfo, op *model.ExecOp) bool {
	if op.IsDir {
		return st.IsDir
	}
	if st.Size != op.DstSize {
		return false
	}
	return op.DstModTime.IsZero() || st.ModTime.Unix() == op.DstModTime.Unix()
}

// Each destination must be unchanged since the run wrote it; anything else is
// skipped and reported. Reverting is idempotent: ops already undone are
// recognized and settled.
func (e *Executor) Revert(ctx context.Context, run *model.AgentRun, target uint) (res *Result, err error) {
	e.run, e.sessionID, e.dirs = run, run.SessionID, map[string]bool{}
	res = &Result{}
	if err := e.Recover(ctx, run.SessionID); err != nil {
		return res, err
	}
	res.Notes = append(res.Notes, e.notes...)
	touched := map[uint]bool{}
	// Reverted ops never come back to a retry, so their entries are unmarked
	// on every exit path, including a cancel.
	defer func() {
		for id := range touched {
			if uerr := e.repo.UnmarkEntryExecuted(id); uerr != nil && err == nil {
				err = uerr
			}
		}
	}()
	ops, err := e.repo.ListExecOps(target)
	if err != nil {
		return res, err
	}
	var todo, mkdirs []model.ExecOp
	for i := len(ops) - 1; i >= 0; i-- {
		switch {
		case ops[i].State != db.ExecDone:
		case ops[i].Kind == db.ExecKindMkdir:
			mkdirs = append(mkdirs, ops[i])
		default:
			todo = append(todo, ops[i])
		}
	}
	if len(todo) == 0 && len(mkdirs) == 0 {
		return res, errors.New("该运行没有可撤销的操作")
	}
	res.Total = len(todo)
	e.emit(res, true)
	for i := range todo {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		op := &todo[i]
		err := e.revertOp(ctx, op)
		var se *skipError
		switch {
		case err == nil:
			res.Success++
			if op.EntryID != 0 {
				touched[op.EntryID] = true
			}
		case errors.As(err, &se):
			res.Skipped++
			res.Issues = append(res.Issues, Issue{EntryID: op.EntryID, OriginalPath: op.Src, NewPath: op.Dst, Code: se.code, Detail: se.detail})
		default:
			res.Failed++
			res.Issues = append(res.Issues, Issue{EntryID: op.EntryID, OriginalPath: op.Src, NewPath: op.Dst, Code: IssueFailed, Detail: err.Error()})
		}
		e.emit(res, false)
	}
	return res, e.removeCreatedDirs(ctx, mkdirs, res)
}

// A directory is removed only when a listing shows it empty; kept directories
// are reported as notes.
func (e *Executor) removeCreatedDirs(ctx context.Context, mkdirs []model.ExecOp, res *Result) error {
	sort.SliceStable(mkdirs, func(i, j int) bool {
		di, dj := strings.Count(mkdirs[i].Dst, "/"), strings.Count(mkdirs[j].Dst, "/")
		if di != dj {
			return di > dj
		}
		return mkdirs[i].ID > mkdirs[j].ID
	})
	removed := 0
	for i := range mkdirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		op := &mkdirs[i]
		st, ok, err := e.stat(ctx, op.Dst)
		if err != nil {
			res.Notes = append(res.Notes, "无法读取目录「"+op.Dst+"」，已保留")
			continue
		}
		if ok {
			if !st.IsDir {
				res.Notes = append(res.Notes, "「"+op.Dst+"」已不是目录，已保留")
				continue
			}
			children, err := e.fs.List(ctx, op.Dst)
			switch {
			case err != nil:
				res.Notes = append(res.Notes, "无法读取目录「"+op.Dst+"」，已保留")
				continue
			case len(children) > 0:
				res.Notes = append(res.Notes, "目录「"+op.Dst+"」非空，已保留")
				continue
			}
			if err := e.fs.Delete(ctx, op.Dst); err != nil {
				res.Notes = append(res.Notes, "删除空目录「"+op.Dst+"」失败，已保留")
				continue
			}
			removed++
		}
		if err := e.setState(op, db.ExecReverted, ""); err != nil {
			return err
		}
	}
	if removed > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("已删除执行时创建的 %d 个空目录", removed))
	}
	return nil
}

func (e *Executor) revertOp(ctx context.Context, op *model.ExecOp) error {
	switch op.Kind {
	case db.ExecKindRmdir:
		if err := e.fs.MkdirAll(ctx, op.Src); err != nil {
			return err
		}
		return e.setState(op, db.ExecReverted, "")
	}

	dst, dstOK, err := e.stat(ctx, op.Dst)
	if err != nil {
		return err
	}
	src, srcOK, err := e.stat(ctx, op.Src)
	if err != nil {
		return err
	}
	if !dstOK {
		if srcOK {
			return e.setState(op, db.ExecReverted, "")
		}
		return skip(IssueSourceMissing, "目标已不存在，无法撤销")
	}
	if !dstUnchanged(dst, op) {
		return skip(IssueSourceChanged, "目标在执行后被修改，未撤销")
	}
	// Reverting a copy only deletes the (unchanged) copy; the source is
	// matched by size alone because it is never touched either way.
	if op.Kind == db.ExecKindCopy && srcOK && src.Size == op.Size {
		if err := e.fs.Delete(ctx, op.Dst); err != nil {
			return err
		}
		return e.setState(op, db.ExecReverted, "")
	}
	if srcOK {
		return skip(IssueTargetExists, "原位置已有同名文件，未撤销")
	}
	if err := e.ensureDir(ctx, op.EntryID, parentOf(op.Src)); err != nil {
		return err
	}
	merr := e.fs.MoveFile(ctx, op.Dst, op.Src)
	if merr != nil {
		if op.IsDir || errors.Is(merr, os.ErrExist) {
			return merr
		}
		// Journaled as a move under the revert run, so a crash is recovered
		// like a forward move.
		back, err := e.journal(op.EntryID, db.ExecKindMove, op.Dst, op.Src, false, dst)
		if err != nil {
			return err
		}
		if err := e.copyJournaled(ctx, back); err != nil {
			return err
		}
	}
	return e.setState(op, db.ExecReverted, "")
}

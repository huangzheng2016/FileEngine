package executor

import (
	"context"
	"errors"
	"fmt"
	"os"

	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// Recover settles ops an interrupted run left in an intermediate state:
// finished steps are completed (a verified move deletes its unchanged
// source), unfinished ones are rolled back (a partial copy is removed).
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
	return nil
}

func (e *Executor) recoverOp(ctx context.Context, op *db.ExecOp) error {
	const interrupted = "中断"
	switch op.Kind {
	case db.ExecKindRename:
		_, srcOK := e.exists(ctx, op.Src)
		_, dstOK := e.exists(ctx, op.Dst)
		if !srcOK && dstOK {
			if err := e.setState(op, db.ExecDone, ""); err != nil {
				return err
			}
			return e.markIfEntryOp(op, ModeMove)
		}
		return e.setState(op, db.ExecFailed, interrupted)
	case db.ExecKindMkdir:
		if st, ok := e.exists(ctx, op.Dst); ok && st.IsDir {
			return e.setState(op, db.ExecDone, "")
		}
		return e.setState(op, db.ExecFailed, interrupted)
	case db.ExecKindRmdir:
		if _, ok := e.exists(ctx, op.Src); !ok {
			return e.setState(op, db.ExecDone, "")
		}
		return e.setState(op, db.ExecFailed, interrupted)
	case db.ExecKindCopy, db.ExecKindMove:
		if op.State == db.ExecPending {
			if _, ok := e.exists(ctx, op.Dst); !ok {
				return e.setState(op, db.ExecFailed, interrupted)
			}
			// The dst did not exist when the op was journaled, so it is ours.
			if err := e.setState(op, db.ExecCopied, ""); err != nil {
				return err
			}
		}
		if err := e.finishCopy(ctx, op); err != nil {
			var se *skipError
			if errors.As(err, &se) || op.State == db.ExecFailed {
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

// markIfEntryOp marks the decided entry executed when op carried out the
// entry itself (not one file inside a directory decision).
func (e *Executor) markIfEntryOp(op *db.ExecOp, mode string) error {
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

func dstUnchanged(st *remotefs.FileInfo, op *db.ExecOp) bool {
	if op.IsDir {
		return st.IsDir
	}
	if st.Size != op.DstSize {
		return false
	}
	return op.DstModTime.IsZero() || st.ModTime.Unix() == op.DstModTime.Unix()
}

// Revert reverses the done ops of target in reverse order within run. Each
// destination must be unchanged since the run wrote it; anything else is
// skipped and reported. Reverting is idempotent: ops already undone are
// recognized and settled.
func (e *Executor) Revert(ctx context.Context, run *db.AgentRun, target uint) (*Result, error) {
	e.run, e.sessionID, e.dirs = run, run.SessionID, map[string]bool{}
	res := &Result{}
	if err := e.Recover(ctx, run.SessionID); err != nil {
		return res, err
	}
	ops, err := e.repo.ListExecOps(target)
	if err != nil {
		return res, err
	}
	var todo []db.ExecOp
	for i := len(ops) - 1; i >= 0; i-- {
		if ops[i].State == db.ExecDone {
			todo = append(todo, ops[i])
		}
	}
	if len(todo) == 0 {
		return res, errors.New("该运行没有可撤销的操作")
	}
	res.Total = len(todo)
	e.emit(res, true)
	touched := map[uint]bool{}
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
	for id := range touched {
		if err := e.repo.UnmarkEntryExecuted(id); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (e *Executor) revertOp(ctx context.Context, op *db.ExecOp) error {
	switch op.Kind {
	case db.ExecKindMkdir:
		if _, ok := e.exists(ctx, op.Dst); ok {
			if err := e.fs.Delete(ctx, op.Dst); err != nil {
				return skip(IssueTargetExists, "目录「"+op.Dst+"」非空，已保留")
			}
		}
		return e.setState(op, db.ExecReverted, "")
	case db.ExecKindRmdir:
		if err := e.fs.MkdirAll(ctx, op.Src); err != nil {
			return err
		}
		return e.setState(op, db.ExecReverted, "")
	}

	dst, dstOK := e.exists(ctx, op.Dst)
	_, srcOK := e.exists(ctx, op.Src)
	if !dstOK {
		if srcOK {
			return e.setState(op, db.ExecReverted, "")
		}
		return skip(IssueSourceMissing, "目标已不存在，无法撤销")
	}
	if !dstUnchanged(dst, op) {
		return skip(IssueSourceChanged, "目标在执行后被修改，未撤销")
	}
	if op.Kind == db.ExecKindCopy && srcOK {
		if src, _ := e.exists(ctx, op.Src); src != nil && src.Size == op.Size {
			if err := e.fs.Delete(ctx, op.Dst); err != nil {
				return err
			}
			return e.setState(op, db.ExecReverted, "")
		}
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
		if err := e.fs.CopyFile(ctx, op.Dst, op.Src); err != nil {
			return err
		}
		if _, err := e.verify(ctx, op.Dst, op.Src, dst.Size); err != nil {
			e.fs.Delete(context.WithoutCancel(ctx), op.Src)
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.fs.Delete(ctx, op.Dst); err != nil {
			return err
		}
	}
	return e.setState(op, db.ExecReverted, "")
}

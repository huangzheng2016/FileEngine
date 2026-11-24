package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"sort"
	"strings"

	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

func (e *Executor) journal(entryID uint, kind, src, dst string, isDir bool, st *remotefs.FileInfo) (*db.ExecOp, error) {
	op := &db.ExecOp{RunID: e.run.ID, SessionID: e.sessionID, EntryID: entryID, Kind: kind,
		Src: src, Dst: dst, IsDir: isDir, State: db.ExecPending}
	if st != nil {
		op.Size, op.ModTime = st.Size, st.ModTime
	}
	return op, e.repo.CreateExecOp(op)
}

func (e *Executor) setState(op *db.ExecOp, state, msg string) error {
	return e.repo.SetExecOpState(op, state, msg)
}

// failOp records a failure that is already being reported to the caller; a
// lost journal write is logged because nothing else can act on it.
func (e *Executor) failOp(op *db.ExecOp, msg string) {
	if err := e.setState(op, db.ExecFailed, msg); err != nil {
		log.Printf("executor: journal op %d failed state: %v", op.ID, err)
	}
}

// stat separates "absent" from "could not tell": only a not-exist error is
// absence, anything else must not be read as permission to act.
func (e *Executor) stat(ctx context.Context, p string) (*remotefs.FileInfo, bool, error) {
	st, err := e.fs.Stat(ctx, p)
	switch {
	case err == nil:
		return st, true, nil
	case remotefs.IsNotExist(err):
		return nil, false, nil
	default:
		return nil, false, err
	}
}

func (e *Executor) ensureDir(ctx context.Context, entryID uint, dir string) error {
	if dir == "" || e.dirs[dir] {
		return nil
	}
	st, ok, err := e.stat(ctx, dir)
	if err != nil {
		return fmt.Errorf("无法确认目录「%s」是否存在：%w", dir, err)
	}
	if ok {
		if !st.IsDir {
			return skip(IssueTargetExists, "目标上级「"+dir+"」是文件")
		}
		e.dirs[dir] = true
		return nil
	}
	if err := e.ensureDir(ctx, entryID, parentOf(dir)); err != nil {
		return err
	}
	op, err := e.journal(entryID, db.ExecKindMkdir, "", dir, true, nil)
	if err != nil {
		return err
	}
	if err := e.fs.MkdirAll(ctx, dir); err != nil {
		e.failOp(op, err.Error())
		return err
	}
	e.dirs[dir] = true
	return e.setState(op, db.ExecDone, "")
}

func (e *Executor) verify(ctx context.Context, src, dst string, size int64) (*remotefs.FileInfo, error) {
	st, err := e.fs.Stat(ctx, dst)
	if err != nil {
		return nil, fmt.Errorf("校验目标失败：%w", err)
	}
	if st.Size != size {
		return nil, fmt.Errorf("校验失败：目标大小 %d，源大小 %d", st.Size, size)
	}
	if size <= e.HashLimit {
		a, err := e.hash(ctx, src)
		if err != nil {
			return nil, err
		}
		b, err := e.hash(ctx, dst)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(a, b) {
			return nil, errors.New("校验失败：内容哈希不一致")
		}
	}
	return st, nil
}

func (e *Executor) hash(ctx context.Context, p string) ([]byte, error) {
	r, err := e.fs.ReadFile(ctx, p)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// finishRename journals a rename that took effect. The op stays pending when
// the destination cannot be read, which lets Recover settle it later.
func (e *Executor) finishRename(ctx context.Context, op *db.ExecOp, st *remotefs.FileInfo) error {
	ds, err := e.fs.Stat(ctx, op.Dst)
	if err != nil {
		return fmt.Errorf("重命名后无法读取目标：%w", err)
	}
	if !st.IsDir && ds.Size != st.Size {
		e.failOp(op, "重命名后大小不一致")
		return fmt.Errorf("重命名后目标大小 %d 与源 %d 不一致", ds.Size, st.Size)
	}
	return e.repo.SetExecOpStateDst(op, db.ExecDone, "", ds.Size, ds.ModTime)
}

// renameLanded answers whether a rename that reported an error actually took
// effect (a lost reply): source gone and destination present.
func (e *Executor) renameLanded(ctx context.Context, op *db.ExecOp) (bool, error) {
	_, srcOK, err := e.stat(ctx, op.Src)
	if err != nil {
		return false, fmt.Errorf("无法确认重命名结果：%w", err)
	}
	_, dstOK, err := e.stat(ctx, op.Dst)
	if err != nil {
		return false, fmt.Errorf("无法确认重命名结果：%w", err)
	}
	return !srcOK && dstOK, nil
}

func (e *Executor) renameOnly(ctx context.Context, entryID uint, src, dst string, st *remotefs.FileInfo) error {
	op, err := e.journal(entryID, db.ExecKindRename, src, dst, st.IsDir, st)
	if err != nil {
		return err
	}
	merr := e.fs.MoveFile(ctx, src, dst)
	if merr == nil {
		return e.finishRename(ctx, op, st)
	}
	if errors.Is(merr, os.ErrExist) {
		e.failOp(op, merr.Error())
		return skip(IssueTargetExists, "目标位置已有同名文件或目录")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	landed, lerr := e.renameLanded(ctx, op)
	switch {
	case lerr != nil:
		return lerr
	case landed:
		return e.finishRename(ctx, op, st)
	}
	e.failOp(op, merr.Error())
	return merr
}

// handled=false means this entry must be copied instead; only a cross-device
// or unsupported rename also disables renaming for the rest of the run.
func (e *Executor) tryRename(ctx context.Context, entryID uint, src, dst string, st *remotefs.FileInfo) (handled bool, err error) {
	if e.renameBroken {
		return false, nil
	}
	op, err := e.journal(entryID, db.ExecKindRename, src, dst, st.IsDir, st)
	if err != nil {
		return true, err
	}
	merr := e.fs.MoveFile(ctx, src, dst)
	if merr == nil {
		return true, e.finishRename(ctx, op, st)
	}
	if errors.Is(merr, os.ErrExist) {
		e.failOp(op, merr.Error())
		return true, skip(IssueTargetExists, "目标位置已有同名文件或目录")
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	landed, lerr := e.renameLanded(ctx, op)
	switch {
	case lerr != nil:
		return true, lerr
	case landed:
		return true, e.finishRename(ctx, op, st)
	}
	e.failOp(op, merr.Error())
	if remotefs.IsCrossDevice(merr) {
		e.renameBroken = true
	}
	return false, nil
}

// A canceled ctx never reaches the delete.
func (e *Executor) transferFile(ctx context.Context, entryID uint, src, dst string, st *remotefs.FileInfo, mode string) error {
	if mode == ModeMove {
		if handled, err := e.tryRename(ctx, entryID, src, dst, st); handled {
			return err
		}
	}
	kind := db.ExecKindCopy
	if mode == ModeMove {
		kind = db.ExecKindMove
	}
	op, err := e.journal(entryID, kind, src, dst, false, st)
	if err != nil {
		return err
	}
	created := remotefs.WithCreateNotice(ctx, func() error { return e.repo.SetExecOpCreated(op) })
	if err := e.fs.CopyFile(created, src, dst); err != nil {
		if op.DstCreated {
			// The backend normally removed its partial file; if that did not
			// happen the dst is still ours, so retry here, and failing that
			// leave the op pending for Recover.
			if derr := e.fs.Delete(context.WithoutCancel(ctx), dst); derr != nil && !remotefs.IsNotExist(derr) {
				e.setState(op, db.ExecPending, "复制失败，未能清理不完整的目标："+derr.Error())
				return err
			}
		}
		e.failOp(op, err.Error())
		if errors.Is(err, os.ErrExist) {
			return skip(IssueTargetExists, "目标位置已有同名文件")
		}
		return err
	}
	return e.finishCopy(ctx, op)
}

// The op is verified and stays journaled; a later Recover may retry the
// source step without blocking anything else.
type unfinishedError struct{ error }

func (e *unfinishedError) Unwrap() error { return e.error }

func (e *Executor) finishCopy(ctx context.Context, op *db.ExecOp) error {
	var ds *remotefs.FileInfo
	if op.State == db.ExecPending {
		var err error
		ds, err = e.verify(ctx, op.Src, op.Dst, op.Size)
		if err != nil {
			if derr := e.fs.Delete(context.WithoutCancel(ctx), op.Dst); derr != nil {
				err = fmt.Errorf("%w（清理目标副本失败：%v）", err, derr)
			}
			e.failOp(op, err.Error())
			return err
		}
		next := db.ExecVerified
		if op.Kind == db.ExecKindCopy {
			next = db.ExecDone
		}
		if err := e.repo.SetExecOpStateDst(op, next, "", ds.Size, ds.ModTime); err != nil {
			return err
		}
	}
	if op.Kind == db.ExecKindCopy {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	st, ok, err := e.stat(ctx, op.Src)
	if err != nil {
		return &unfinishedError{fmt.Errorf("无法确认源文件状态：%w", err)}
	}
	if ok {
		if st.Size != op.Size || (!op.ModTime.IsZero() && st.ModTime.Unix() != op.ModTime.Unix()) {
			e.setState(op, db.ExecVerified, "源文件在复制后被修改，未删除源")
			return skip(IssueSourceChanged, "源文件在复制后被修改，已保留两份")
		}
		if ds == nil {
			// Resumed from the journal: the copy may have changed or vanished.
			var dok bool
			ds, dok, err = e.stat(ctx, op.Dst)
			if err != nil {
				return &unfinishedError{fmt.Errorf("无法确认副本状态：%w", err)}
			}
			if !dok || !dstUnchanged(ds, op) {
				msg := "副本已缺失或被修改，未删除源"
				e.failOp(op, msg)
				return skip(IssueSourceChanged, msg)
			}
		}
		if err := e.fs.Delete(ctx, op.Src); err != nil {
			e.setState(op, db.ExecVerified, "删除源失败："+err.Error())
			return &unfinishedError{fmt.Errorf("副本已校验，但删除源失败：%w", err)}
		}
	}
	return e.setState(op, db.ExecDone, "")
}

func (e *Executor) transferDir(ctx context.Context, entryID uint, src, dst, mode string, resume bool) error {
	if mode == ModeMove && !resume {
		st, err := e.fs.Stat(ctx, src)
		if err != nil {
			return err
		}
		if handled, err := e.tryRename(ctx, entryID, src, dst, st); handled {
			return err
		}
	}
	return e.walk(ctx, entryID, src, dst, mode)
}

func (e *Executor) walk(ctx context.Context, entryID uint, src, dst, mode string) error {
	if err := e.ensureDir(ctx, entryID, dst); err != nil {
		return err
	}
	children, err := e.fs.List(ctx, src)
	if err != nil {
		return err
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name < children[j].Name })
	var errs []error
	for i := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := &children[i]
		// Names come from the server: a non-segment name could steer the walk
		// outside src/dst.
		if c.Name == "" || c.Name == "." || c.Name == ".." || strings.ContainsAny(c.Name, "/\\\x00") {
			errs = append(errs, fmt.Errorf("illegal entry name %q in %s", c.Name, src))
			continue
		}
		s, d := path.Join(src, c.Name), path.Join(dst, c.Name)
		var err error
		switch {
		case c.IsDir:
			err = e.walk(ctx, entryID, s, d, mode)
		case c.FileType == remotefs.FileTypeSymlink:
			if mode == ModeCopy {
				continue
			}
			err = e.renameOnly(ctx, entryID, s, d, c)
		default:
			err = e.walkFile(ctx, entryID, s, d, c, mode)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			errs = append(errs, fmt.Errorf("%s: %w", s, err))
		}
	}
	if len(errs) > 0 {
		var se *skipError
		code := ""
		for _, err := range errs {
			if !errors.As(err, &se) {
				code = ""
				break
			}
			if code == "" {
				code = se.code
			}
		}
		msg := fmt.Sprintf("%d 项未完成，源目录已保留：%s", len(errs), errors.Join(errs...).Error())
		if code != "" {
			return skip(code, msg)
		}
		return errors.New(msg)
	}
	if mode == ModeMove {
		return e.removeEmptyDir(ctx, entryID, src)
	}
	return nil
}

func (e *Executor) walkFile(ctx context.Context, entryID uint, s, d string, c *remotefs.FileInfo, mode string) error {
	_, ok, err := e.stat(ctx, d)
	if err != nil {
		return err
	}
	if ok {
		if op, _ := e.repo.FindDoneExecOp(e.sessionID, d); op != nil && op.Src == s && op.Kind == db.ExecKindCopy {
			return nil
		}
		return skip(IssueTargetExists, "目标位置已有同名文件："+d)
	}
	if row, err := e.repo.GetFileByPath(e.sessionID, s); err == nil && row != nil {
		if reason := staleReason(row, c); reason != "" {
			return skip(IssueSourceChanged, reason)
		}
	}
	return e.transferFile(ctx, entryID, s, d, c, mode)
}

// Delete never removes a non-empty directory, so leftovers keep it in place.
func (e *Executor) removeEmptyDir(ctx context.Context, entryID uint, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	op, err := e.journal(entryID, db.ExecKindRmdir, dir, "", true, nil)
	if err != nil {
		return err
	}
	if err := e.fs.Delete(ctx, dir); err != nil {
		e.setState(op, db.ExecFailed, err.Error())
		return fmt.Errorf("源目录未清空，已保留：%w", err)
	}
	return e.setState(op, db.ExecDone, "")
}

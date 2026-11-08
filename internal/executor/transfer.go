package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"

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

func (e *Executor) exists(ctx context.Context, p string) (*remotefs.FileInfo, bool) {
	st, err := e.fs.Stat(ctx, p)
	return st, err == nil
}

// ensureDir makes dir exist, journaling every level it creates.
func (e *Executor) ensureDir(ctx context.Context, entryID uint, dir string) error {
	if dir == "" || e.dirs[dir] {
		return nil
	}
	if st, ok := e.exists(ctx, dir); ok {
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
		e.setState(op, db.ExecFailed, err.Error())
		return err
	}
	e.dirs[dir] = true
	return e.setState(op, db.ExecDone, "")
}

// verify checks dst against the source: size always, content hash up to
// HashLimit. It returns dst's stat.
func (e *Executor) verify(ctx context.Context, src, dst string, size int64) (*remotefs.FileInfo, error) {
	st, err := e.fs.Stat(ctx, dst)
	if err != nil {
		return nil, fmt.Errorf("校验目标失败: %w", err)
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

// renameOnly moves src by in-place rename with no copy fallback.
func (e *Executor) renameOnly(ctx context.Context, entryID uint, src, dst string, st *remotefs.FileInfo) error {
	op, err := e.journal(entryID, db.ExecKindRename, src, dst, st.IsDir, st)
	if err != nil {
		return err
	}
	if err := e.fs.MoveFile(ctx, src, dst); err != nil {
		e.setState(op, db.ExecFailed, err.Error())
		if errors.Is(err, os.ErrExist) {
			return skip(IssueTargetExists, "目标位置已有同名文件或目录")
		}
		return err
	}
	if dst, ok := e.exists(ctx, dst); ok {
		e.repo.SetExecOpDstStat(op, dst.Size, dst.ModTime)
	}
	return e.setState(op, db.ExecDone, "")
}

// tryRename attempts an in-place rename. handled=false means the backend
// could not rename (e.g. cross-device) and the caller must copy instead.
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
		if ds, ok := e.exists(ctx, dst); ok {
			if !st.IsDir && ds.Size != st.Size {
				e.setState(op, db.ExecFailed, "重命名后大小不一致")
				return true, fmt.Errorf("重命名后目标大小 %d 与源 %d 不一致", ds.Size, st.Size)
			}
			e.repo.SetExecOpDstStat(op, ds.Size, ds.ModTime)
		}
		return true, e.setState(op, db.ExecDone, "")
	}
	e.setState(op, db.ExecFailed, merr.Error())
	if errors.Is(merr, os.ErrExist) {
		return true, skip(IssueTargetExists, "目标位置已有同名文件或目录")
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	e.renameBroken = true
	return false, nil
}

// transferFile copies or moves one regular file. A move renames in place
// when possible; otherwise it copies, verifies, and only then deletes the
// source. A canceled ctx never reaches the delete.
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
	if err := e.fs.CopyFile(ctx, src, dst); err != nil {
		e.setState(op, db.ExecFailed, err.Error())
		if errors.Is(err, os.ErrExist) {
			return skip(IssueTargetExists, "目标位置已有同名文件")
		}
		return err
	}
	if err := e.setState(op, db.ExecCopied, ""); err != nil {
		return err
	}
	return e.finishCopy(ctx, op)
}

// finishCopy verifies a copied op and, for moves, deletes the source.
func (e *Executor) finishCopy(ctx context.Context, op *db.ExecOp) error {
	if op.State == db.ExecCopied {
		ds, err := e.verify(ctx, op.Src, op.Dst, op.Size)
		if err != nil {
			e.fs.Delete(context.WithoutCancel(ctx), op.Dst)
			e.setState(op, db.ExecFailed, err.Error())
			return err
		}
		e.repo.SetExecOpDstStat(op, ds.Size, ds.ModTime)
		if err := e.setState(op, db.ExecVerified, ""); err != nil {
			return err
		}
	}
	if op.Kind == db.ExecKindCopy {
		return e.setState(op, db.ExecDone, "")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if st, ok := e.exists(ctx, op.Src); ok {
		if st.Size != op.Size || (!op.ModTime.IsZero() && st.ModTime.Unix() != op.ModTime.Unix()) {
			e.setState(op, db.ExecVerified, "源文件在复制后被修改，未删除源")
			return skip(IssueSourceChanged, "源文件在复制后被修改，已保留两份")
		}
		if err := e.fs.Delete(ctx, op.Src); err != nil {
			e.setState(op, db.ExecVerified, "删除源失败: "+err.Error())
			return fmt.Errorf("副本已校验，但删除源失败: %w", err)
		}
	}
	return e.setState(op, db.ExecDone, "")
}

// transferDir carries out a directory decision: an atomic rename when
// moving and possible, otherwise a file-by-file walk. resume continues a
// directory an earlier run already started.
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
		msg := fmt.Sprintf("%d 项未完成，源目录已保留: %s", len(errs), errors.Join(errs...).Error())
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
	if _, ok := e.exists(ctx, d); ok {
		if op, _ := e.repo.FindDoneExecOp(e.sessionID, d); op != nil && op.Src == s && op.Kind == db.ExecKindCopy {
			return nil
		}
		return skip(IssueTargetExists, "目标位置已有同名文件: "+d)
	}
	if row, err := e.repo.GetFileByPath(e.sessionID, s); err == nil && row != nil {
		if reason := staleReason(row, c); reason != "" {
			return skip(IssueSourceChanged, reason)
		}
	}
	return e.transferFile(ctx, entryID, s, d, c, mode)
}

// removeEmptyDir removes a source directory the move emptied. Delete never
// removes a non-empty directory, so leftovers keep it in place.
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
		return fmt.Errorf("源目录未清空，已保留: %w", err)
	}
	return e.setState(op, db.ExecDone, "")
}

package executor

import (
	"context"
	"errors"
	"testing"

	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

func TestRecoverPendingForeignDstIsKept(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	fa := e.entry("a.txt", "x/a.txt")
	run, _ := e.journalOp(db.ExecKindMove, "a.txt", "x/a.txt", db.ExecPending, fa.ID, false)
	e.write("x/a.txt", "user file, not ours")
	if err := New(e.repo, e.fs).Recover(context.Background(), e.sid); err != nil {
		t.Fatal(err)
	}
	if s, ok := e.read("x/a.txt"); !ok || s != "user file, not ours" {
		t.Fatal("foreign destination was deleted or replaced")
	}
	if !e.exists("a.txt") {
		t.Fatal("source must stay")
	}
	ops, _ := e.repo.ListExecOps(run.ID)
	if ops[0].State != db.ExecFailed {
		t.Fatalf("op state %s", ops[0].State)
	}
}

func TestFailedCopyRemovesItsOwnPartialDst(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	fa := e.entry("a.txt", "x/a.txt")
	fs := &noticeFS{faultFS: e.fs}
	run, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "t")
	res, err := New(e.repo, fs).Execute(context.Background(), run, Options{Mode: ModeCopy})
	if err != nil || res.Failed != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if e.exists("x/a.txt") || e.executed(fa.ID) {
		t.Fatal("partial destination left behind")
	}
}

// noticeFS copies, announces the create like a real backend, then fails
// without removing the dst.
type noticeFS struct{ *faultFS }

func (f *noticeFS) CopyFile(ctx context.Context, src, dst string) error {
	if err := f.faultFS.RemoteFS.CopyFile(ctx, src, dst); err != nil {
		return err
	}
	return errors.New("i/o timeout")
}

var _ remotefs.RemoteFS = (*noticeFS)(nil)

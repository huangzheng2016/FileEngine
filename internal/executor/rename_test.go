package executor

import (
	"context"
	"errors"
	"os"
	"testing"

	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// flakyRename fails its first MoveFile; when landed is set the rename still
// takes effect first, like a reply lost on the wire.
type flakyRename struct {
	*faultFS
	err    error
	landed bool
	n      int
}

func (f *flakyRename) MoveFile(ctx context.Context, src, dst string) error {
	f.n++
	if f.n == 1 {
		if f.landed {
			if err := f.faultFS.MoveFile(ctx, src, dst); err != nil {
				return err
			}
		}
		return f.err
	}
	return f.faultFS.MoveFile(ctx, src, dst)
}

func TestTransientRenameErrorKeepsRenameEnabled(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	e.write("b.txt", "BBBB")
	e.entry("a.txt", "x/a.txt")
	e.entry("b.txt", "x/b.txt")
	fs := &flakyRename{faultFS: e.fs, err: errors.New("i/o timeout")}
	run, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "t")
	res, err := New(e.repo, fs).Execute(context.Background(), run, Options{Mode: ModeMove})
	if err != nil || res.Success != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	ops, _ := e.repo.ListExecOps(run.ID)
	for _, op := range ops {
		if op.Kind == db.ExecKindMove && op.Src == "b.txt" {
			t.Fatalf("b.txt went through copy after one transient error: %+v", op)
		}
	}
}

func TestCrossDeviceRenameFallsBackToCopyForTheRest(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	e.write("b.txt", "BBBB")
	e.entry("a.txt", "x/a.txt")
	e.entry("b.txt", "x/b.txt")
	e.fs.noRename = true
	run, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "t")
	res, err := New(e.repo, e.fs).Execute(context.Background(), run, Options{Mode: ModeMove})
	if err != nil || res.Success != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	ops, _ := e.repo.ListExecOps(run.ID)
	renames := 0
	for _, op := range ops {
		if op.Kind == db.ExecKindRename {
			renames++
		}
	}
	if renames != 1 {
		t.Fatalf("rename attempted %d times, want 1", renames)
	}
}

func TestRenameThatLandedDespiteErrorIsDoneAndRevertible(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	fa := e.entry("a.txt", "x/a.txt")
	fs := &flakyRename{faultFS: e.fs, err: errors.New("i/o timeout"), landed: true}
	run, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "t")
	res, err := New(e.repo, fs).Execute(context.Background(), run, Options{Mode: ModeMove})
	if err != nil || res.Success != 1 || !e.executed(fa.ID) {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	e.repo.FinishRun(run, db.RunDone, "", "", db.TokenCounts{})
	rv, _ := e.repo.CreateRun(e.sid, db.RunPhaseRevert, "rv")
	if _, err := New(e.repo, e.fs).Revert(context.Background(), rv, run.ID); err != nil {
		t.Fatal(err)
	}
	if !e.exists("a.txt") || e.exists("x/a.txt") {
		t.Fatal("revert did not restore the file")
	}
}

type statErrFS struct {
	*faultFS
	path string
}

func (f statErrFS) Stat(ctx context.Context, p string) (*remotefs.FileInfo, error) {
	if p == f.path {
		return nil, errors.New("i/o timeout")
	}
	return f.faultFS.Stat(ctx, p)
}

func TestTransientStatErrorIsNotTreatedAsAbsent(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	e.write("x/keep.txt", "K")
	fa := e.entry("a.txt", "x/a.txt")
	run, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "t")
	res, err := New(e.repo, statErrFS{e.fs, "x"}).Execute(context.Background(), run, Options{Mode: ModeMove})
	if err != nil || res.Failed != 1 || e.executed(fa.ID) {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	ops, _ := e.repo.ListExecOps(run.ID)
	for _, op := range ops {
		if op.Kind == db.ExecKindMkdir {
			t.Fatalf("mkdir journaled for an existing directory: %+v", op)
		}
	}
	if !e.exists("a.txt") {
		t.Fatal("source must stay")
	}
	_ = os.ErrNotExist
}

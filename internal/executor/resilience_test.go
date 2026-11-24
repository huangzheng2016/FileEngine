package executor

import (
	"context"
	"errors"
	"testing"

	"FileEngine/internal/db"
)

type noDeleteFS struct {
	*faultFS
	path string
}

func (f noDeleteFS) Delete(ctx context.Context, p string) error {
	if p == f.path {
		return errors.New("permission denied")
	}
	return f.faultFS.Delete(ctx, p)
}

func TestFailedSourceDeleteDoesNotBlockLaterRuns(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	e.write("b.txt", "BBBB")
	e.entry("a.txt", "x/a.txt")
	e.fs.noRename = true
	fs := noDeleteFS{e.fs, "a.txt"}
	run, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "t")
	res, err := New(e.repo, fs).Execute(context.Background(), run, Options{Mode: ModeMove})
	if err != nil || res.Failed != 1 {
		t.Fatalf("first run: res=%+v err=%v", res, err)
	}
	e.repo.FinishRun(run, db.RunPartial, "", "", db.TokenCounts{})
	fb := e.entry("b.txt", "x/b.txt")
	run2, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "t2")
	res, err = New(e.repo, fs).Execute(context.Background(), run2, Options{Mode: ModeMove})
	if err != nil || res.Success != 1 || !e.executed(fb.ID) {
		t.Fatalf("second execute blocked: res=%+v err=%v", res, err)
	}
	e.repo.FinishRun(run2, db.RunDone, "", "", db.TokenCounts{})
	rv, _ := e.repo.CreateRun(e.sid, db.RunPhaseRevert, "rv")
	if _, err := New(e.repo, fs).Revert(context.Background(), rv, run2.ID); err != nil {
		t.Fatalf("revert blocked: %v", err)
	}
}

type cancelAfterMove struct {
	*faultFS
	cancel func()
	n      int
}

func (f *cancelAfterMove) MoveFile(ctx context.Context, src, dst string) error {
	err := f.faultFS.MoveFile(ctx, src, dst)
	f.n++
	if f.n == 3 { // two forward moves, then the first revert move
		f.cancel()
	}
	return err
}

func TestCancelledRevertUnmarksRevertedEntries(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "A")
	e.write("b.txt", "B")
	fa := e.entry("a.txt", "x/a.txt")
	fb := e.entry("b.txt", "x/b.txt")
	ctx, cancel := context.WithCancel(context.Background())
	fs := &cancelAfterMove{faultFS: e.fs, cancel: cancel}
	run, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "t")
	if _, err := New(e.repo, fs).Execute(context.Background(), run, Options{Mode: ModeMove}); err != nil {
		t.Fatal(err)
	}
	e.repo.FinishRun(run, db.RunDone, "", "", db.TokenCounts{})
	rv, _ := e.repo.CreateRun(e.sid, db.RunPhaseRevert, "rv")
	if _, err := New(e.repo, fs).Revert(ctx, rv, run.ID); err == nil {
		t.Fatal("revert should report the cancellation")
	}
	e.repo.FinishRun(rv, db.RunStopped, "", "", db.TokenCounts{})
	rv2, _ := e.repo.CreateRun(e.sid, db.RunPhaseRevert, "rv2")
	if _, err := New(e.repo, e.fs).Revert(context.Background(), rv2, run.ID); err != nil {
		t.Fatal(err)
	}
	for _, f := range []*db.FileEntry{fa, fb} {
		if e.executed(f.ID) {
			t.Fatalf("entry %s still executed after revert", f.OriginalPath)
		}
	}
}

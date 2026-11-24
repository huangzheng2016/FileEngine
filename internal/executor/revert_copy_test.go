package executor

import (
	"context"
	"errors"
	"testing"

	"FileEngine/internal/db"
)

// revertMoveFails makes every MoveFile after the first n fail.
type revertMoveFails struct {
	*faultFS
	n, calls int
}

func (f *revertMoveFails) MoveFile(ctx context.Context, src, dst string) error {
	f.calls++
	if f.calls > f.n {
		return errors.New("rename: cross-device link")
	}
	return f.faultFS.MoveFile(ctx, src, dst)
}

func TestRevertCopyFallbackIsJournaled(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	fa := e.entry("a.txt", "x/a.txt")
	fs := &revertMoveFails{faultFS: e.fs, n: 1}
	run, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "t")
	if res, err := New(e.repo, fs).Execute(context.Background(), run, Options{Mode: ModeMove}); err != nil || res.Success != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	e.repo.FinishRun(run, db.RunDone, "", "", db.TokenCounts{})
	rv, _ := e.repo.CreateRun(e.sid, db.RunPhaseRevert, "rv")
	if _, err := New(e.repo, fs).Revert(context.Background(), rv, run.ID); err != nil {
		t.Fatal(err)
	}
	if !e.exists("a.txt") || e.exists("x/a.txt") || e.executed(fa.ID) {
		t.Fatal("revert by copy did not restore the file")
	}
	ops, _ := e.repo.ListExecOps(rv.ID)
	found := false
	for _, op := range ops {
		if op.Kind == db.ExecKindMove && op.Src == "x/a.txt" && op.Dst == "a.txt" && op.State == db.ExecDone && op.DstCreated {
			found = true
		}
	}
	if !found {
		t.Fatalf("copy fallback not journaled under the revert run: %+v", ops)
	}
}

func TestRecoverRemovesPartialFileOfInterruptedRevertCopy(t *testing.T) {
	e := newEnv(t)
	e.write("x/a.txt", "AAAA")
	fa := e.entry("x/a.txt", "")
	e.write("a.txt", "AA")
	run, _ := e.repo.CreateRun(e.sid, db.RunPhaseRevert, "crashed revert")
	st, mt := e.stat("x/a.txt")
	if err := e.repo.CreateExecOp(&db.ExecOp{RunID: run.ID, SessionID: e.sid, EntryID: fa.ID, Kind: db.ExecKindMove,
		Src: "x/a.txt", Dst: "a.txt", Size: st, ModTime: mt, DstCreated: true, State: db.ExecPending}); err != nil {
		t.Fatal(err)
	}
	if err := New(e.repo, e.fs).Recover(context.Background(), e.sid); err != nil {
		t.Fatal(err)
	}
	if e.exists("a.txt") || !e.exists("x/a.txt") {
		t.Fatal("partial file at the original location must be removed, copy kept")
	}
}

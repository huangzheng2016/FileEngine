package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/db"
)

func (e *env) stat(p string) (int64, time.Time) {
	e.t.Helper()
	s, err := os.Stat(filepath.Join(e.root, p))
	if err != nil {
		e.t.Fatal(err)
	}
	return s.Size(), s.ModTime()
}

func (e *env) journalOp(kind, src, dst, state string, entryID uint, withDstStat bool) (*model.AgentRun, *model.ExecOp) {
	e.t.Helper()
	run, err := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "crashed")
	if err != nil {
		e.t.Fatal(err)
	}
	sa, ma := e.stat(src)
	op := &model.ExecOp{RunID: run.ID, SessionID: e.sid, EntryID: entryID, Kind: kind,
		Src: src, Dst: dst, Size: sa, ModTime: ma, State: state}
	if withDstStat {
		op.DstSize = sa
	}
	if err := e.repo.CreateExecOp(op); err != nil {
		e.t.Fatal(err)
	}
	return run, op
}

func TestRecoverVerifiedMoveKeepsSourceWhenDstMissing(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	fa := e.entry("a.txt", "x/a.txt")
	run, _ := e.journalOp(db.ExecKindMove, "a.txt", "x/a.txt", db.ExecVerified, fa.ID, true)
	if err := New(e.repo, e.fs).Recover(context.Background(), e.sid); err != nil {
		t.Fatal(err)
	}
	if !e.exists("a.txt") {
		t.Fatal("source deleted although the copy is gone")
	}
	ops, _ := e.repo.ListExecOps(run.ID)
	if ops[0].State != db.ExecFailed || ops[0].Error == "" {
		t.Fatalf("op = %s %q", ops[0].State, ops[0].Error)
	}
}

func TestRecoverVerifiedMoveKeepsSourceWhenDstChanged(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	e.write("x/a.txt", "edited by user, longer")
	fa := e.entry("a.txt", "x/a.txt")
	e.journalOp(db.ExecKindMove, "a.txt", "x/a.txt", db.ExecVerified, fa.ID, true)
	if err := New(e.repo, e.fs).Recover(context.Background(), e.sid); err != nil {
		t.Fatal(err)
	}
	if !e.exists("a.txt") || !e.exists("x/a.txt") {
		t.Fatal("a file was deleted although the copy changed")
	}
}

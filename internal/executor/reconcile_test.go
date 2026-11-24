package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"FileEngine/internal/db"
)

// A crash between an op's final done and MarkEntryExecuted must not strand
// the entry as planned.
func TestRecoverMarksEntryOfFinishedOp(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	fa := e.entry("a.txt", "x/a.txt")
	run, op := e.journalOp(db.ExecKindMove, "a.txt", "x/a.txt", db.ExecDone, fa.ID, true)
	if err := os.MkdirAll(filepath.Join(e.root, "x"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(e.root, "a.txt"), filepath.Join(e.root, "x/a.txt")); err != nil {
		t.Fatal(err)
	}
	_ = run
	_ = op
	if err := New(e.repo, e.fs).Recover(context.Background(), e.sid); err != nil {
		t.Fatal(err)
	}
	if !e.executed(fa.ID) {
		t.Fatal("entry of a finished op is still planned")
	}
}

func TestRecoverDoesNotMarkEntryOfRevertedOp(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	fa := e.entry("a.txt", "x/a.txt")
	e.journalOp(db.ExecKindMove, "a.txt", "x/a.txt", db.ExecReverted, fa.ID, true)
	if err := New(e.repo, e.fs).Recover(context.Background(), e.sid); err != nil {
		t.Fatal(err)
	}
	if e.executed(fa.ID) {
		t.Fatal("reverted op marked its entry executed")
	}
}

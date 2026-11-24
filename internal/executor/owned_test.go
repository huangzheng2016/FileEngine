package executor

import (
	"context"
	"testing"

	"FileEngine/internal/db"
)

func TestDirIsNotMergedIntoDirMadeForAnotherEntry(t *testing.T) {
	e := newEnv(t)
	e.write("in/a.txt", "A")
	e.write("proj/main.go", "package main")
	e.write("out/proj/other.txt", "O")
	fa := e.entry("in/a.txt", "out/proj/a.txt")
	run, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "earlier")
	// A mkdir journaled as part of another entry's move.
	e.repo.CreateExecOp(&db.ExecOp{RunID: run.ID, SessionID: e.sid, EntryID: fa.ID, Kind: db.ExecKindMkdir,
		Dst: "out/proj", IsDir: true, State: db.ExecDone})
	e.repo.FinishRun(run, db.RunDone, "", "", db.TokenCounts{})
	fp := e.entry("proj", "out/proj")
	res, err := e.run(context.Background(), Options{Mode: ModeMove, CategoryPath: ""})
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(res, IssueTargetExists) || e.executed(fp.ID) || !e.exists("proj/main.go") {
		t.Fatalf("directory merged into a foreign directory: %+v", res)
	}
}

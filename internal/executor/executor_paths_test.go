package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Plans written straight to the DB (bypassing every validator) with an
// escaping target are refused before any filesystem operation.
func TestEscapingTargetNeverExecuted(t *testing.T) {
	e := newEnv(t)
	e.write("ok.txt", "ok")
	e.write("evil.txt", "evil")
	ok := e.entry("ok.txt", "sorted/ok.txt")
	evil := e.entry("evil.txt", "")
	if err := e.repo.DB().Model(evil).Updates(map[string]any{"new_path": "/../escaped.txt", "operation": "planned"}).Error; err != nil {
		t.Fatal(err)
	}

	res, err := e.run(context.Background(), Options{Mode: ModeCopy})
	if err != nil {
		t.Fatal(err)
	}
	if !e.executed(ok.ID) || e.executed(evil.ID) {
		t.Fatalf("executed: ok=%v evil=%v", e.executed(ok.ID), e.executed(evil.ID))
	}
	if !hasIssue(res, IssueInvalidTarget) {
		t.Fatalf("escaping target not flagged: %+v", res.Issues)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.root), "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("file written outside the root (stat err %v)", err)
	}
}

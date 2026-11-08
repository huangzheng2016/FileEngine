package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// The executor re-validates every plan right before the operation: rows with
// an escaping source or target fail without touching the filesystem, legal
// rows run with root-relative paths (a leading "/" means the BasePath root).
func TestExecutorRevalidatesPlanPaths(t *testing.T) {
	repo := setupTestDB(t)
	parent := t.TempDir()
	base := filepath.Join(parent, "base")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ok.txt", "evil.txt"} {
		if err := os.WriteFile(filepath.Join(base, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := remotefs.NewLocalFS(base)
	if err != nil {
		t.Fatal(err)
	}
	sess := &db.ScanSession{Status: "tagged"}
	if err := repo.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	rows := []db.FileEntry{
		{ScanSessionID: sess.ID, OriginalPath: "ok.txt", NewPath: "/sorted/ok.txt", Operation: "planned", Name: "ok.txt", FileType: "file"},
		// Written straight to the DB, bypassing every API/tool validator.
		{ScanSessionID: sess.ID, OriginalPath: "evil.txt", NewPath: "../escaped.txt", Operation: "planned", Name: "evil.txt", FileType: "file"},
		{ScanSessionID: sess.ID, OriginalPath: "../outside.txt", NewPath: "/sorted/outside.txt", Operation: "planned", Name: "outside.txt", FileType: "file"},
	}
	for i := range rows {
		if err := repo.CreateFile(&rows[i]); err != nil {
			t.Fatal(err)
		}
	}

	ex := New(repo, fs)
	if err := ex.Execute(context.Background(), sess.ID, "copy"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	p := ex.Progress()
	if p.Success != 1 || p.Failed != 2 {
		t.Fatalf("progress = %+v, want 1 success / 2 failed", p)
	}
	if _, err := os.Stat(filepath.Join(base, "sorted", "ok.txt")); err != nil {
		t.Fatalf("legal plan not executed under base: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("escaping target was written outside base (stat err %v)", err)
	}

	v, err := ex.Validate(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, is := range v.Issues {
		if is.Issue == "invalid" && is.NewPath == "../escaped.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Validate did not flag the escaping target: %+v", v.Issues)
	}
}

package scanner

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"FileEngine/internal/db"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupFileTestDB mirrors setupTestDB but backs the store with a temp file:
// the progress tests poll the session row from one goroutine while the scan
// runs on another, and ":memory:" gives every pooled connection its own
// private database.
func setupFileTestDB(t *testing.T) *db.Repository {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gdb.AutoMigrate(&db.FileEntry{}, &db.ScanSession{}, &db.Category{}, &db.Filesystem{}, &db.AgentLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db.NewRepository(gdb)
}

// A finished scan leaves the session row coherent: scanned_count converged to
// total_files, the path heartbeat cleared, and the live counters match the
// persisted shape.
func TestScanProgress_FinalState(t *testing.T) {
	fs := &fakeFS{root: testTree()}
	repo := setupFileTestDB(t)
	session := &db.ScanSession{ID: 1, RootPath: "root", Status: "scanning"}
	if err := repo.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sc := New(fs, repo, "", nil)
	if err := sc.Scan(context.Background(), session); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	want := len(wantPaths())
	if session.ScannedCount != want || session.TotalFiles != want {
		t.Errorf("session counters = scanned %d / total %d, want %d/%d",
			session.ScannedCount, session.TotalFiles, want, want)
	}
	// The in-memory counters agree: 12 entries, 5 listed dirs (root, photos,
	// raw, docs, sub — junk dirs are pruned before listing).
	if sc.progress.Entries() != int64(want) {
		t.Errorf("progress entries = %d, want %d", sc.progress.Entries(), want)
	}
	if sc.progress.Dirs() != 5 {
		t.Errorf("progress dirs = %d, want 5", sc.progress.Dirs())
	}

	row, err := repo.GetSession(1)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.ScannedCount != want || row.CurrentPath != "" || row.ErrorMessage != "" {
		t.Errorf("persisted row = scanned %d path %q err %q, want %d / empty / empty",
			row.ScannedCount, row.CurrentPath, row.ErrorMessage, want)
	}
}

// While the walk runs, the session row must reflect live progress — the whole
// point: total_files only exists after the full tree, scanned_count is now.
func TestScanProgress_LiveRowUpdates(t *testing.T) {
	prev := progressReportInterval
	progressReportInterval = 5 * time.Millisecond
	defer func() { progressReportInterval = prev }()

	// A tree wide enough that the walk outlives several reporter ticks.
	children := make([]*memNode, 0, 101)
	for i := 0; i < 100; i++ {
		children = append(children, dir(fmt.Sprintf("d%03d", i), file("a"), file("b")))
	}
	hfs := &fakeHandleFS{fakeFS: &fakeFS{root: &memNode{name: "root", isDir: true, children: children}}}

	repo := setupFileTestDB(t)
	session := &db.ScanSession{ID: 1, RootPath: "root", Status: "scanning"}
	if err := repo.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sc := New(hfs, repo, "", nil)

	done := make(chan error, 1)
	go func() { done <- sc.Scan(context.Background(), session) }()

	deadline := time.Now().Add(10 * time.Second)
	sawLive := false
	for time.Now().Before(deadline) {
		row, err := repo.GetSession(1)
		if err == nil && row.Status == "scanning" && row.ScannedCount > 0 && row.CurrentPath != "" {
			sawLive = true
			break
		}
		select {
		case err := <-done:
			t.Fatalf("scan finished before live progress was observed: %v", err)
		case <-time.After(2 * time.Millisecond):
		}
	}
	if !sawLive {
		t.Fatal("never observed scanned_count > 0 + current_path on the session row mid-scan")
	}
	if err := <-done; err != nil {
		t.Fatalf("Scan: %v", err)
	}
}

// A walk failure still leaves the partial count on the session struct — the
// API layer persists it together with the error, so "interrupted at N
// entries" is visible instead of a bare failure.
func TestScanProgress_FailureKeepsPartialCount(t *testing.T) {
	fs := &fakeFS{root: testTree(), failOn: "photos"}
	repo := setupFileTestDB(t)
	session := &db.ScanSession{ID: 1, RootPath: "root", Status: "scanning"}
	if err := repo.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sc := New(fs, repo, "", nil)
	err := sc.Scan(context.Background(), session)
	if err == nil || !strings.Contains(err.Error(), "simulated list failure") {
		t.Fatalf("err = %v, want the list failure", err)
	}
	if session.ScannedCount == 0 {
		t.Error("ScannedCount = 0, want the partial count collected before the failure")
	}
	if session.ScannedCount >= len(wantPaths()) {
		t.Errorf("ScannedCount = %d, want less than the full %d (walk aborted)", session.ScannedCount, len(wantPaths()))
	}
	if session.Status == "scanned" {
		t.Error("session must not be marked scanned after a listing failure")
	}
}

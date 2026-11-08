package scanner

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"

	"github.com/glebarez/sqlite"
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
	if err := gdb.AutoMigrate(&db.FileEntry{}, &db.ScanSession{}, &db.Category{}, &db.Filesystem{}); err != nil {
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

// gatedFS blocks the listing of one directory until released, so a test can
// observe the session row while the walk is provably still running.
type gatedFS struct {
	*fakeFS
	gate    string
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedFS) List(ctx context.Context, p string) ([]remotefs.FileInfo, error) {
	if p == g.gate {
		g.once.Do(func() { close(g.reached) })
		<-g.release
	}
	return g.fakeFS.List(ctx, p)
}

func TestScanProgress_LiveRowUpdates(t *testing.T) {
	prev := progressReportInterval
	progressReportInterval = time.Millisecond
	defer func() { progressReportInterval = prev }()

	gfs := &gatedFS{
		fakeFS:  &fakeFS{root: testTree()},
		gate:    "root/photos",
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
	repo := setupFileTestDB(t)
	session := &db.ScanSession{ID: 1, RootPath: "root", Status: "scanning"}
	if err := repo.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sc := New(gfs, repo, "", nil)

	done := make(chan error, 1)
	go func() { done <- sc.Scan(context.Background(), session) }()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gfs.release) }) }
	defer release()

	select {
	case <-gfs.reached:
	case err := <-done:
		t.Fatalf("scan finished without listing the gated directory: %v", err)
	}

	var row *db.ScanSession
	for {
		var err error
		row, err = repo.GetSession(1)
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		if row.ScannedCount > 0 && row.CurrentPath != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if row.Status != "scanning" {
		t.Errorf("status = %q mid-scan, want scanning", row.Status)
	}

	release()
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

package scanner

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// The production SQLite setup (busy timeout, WAL, one writer connection):
// the progress tests poll the session row while the scan writes on another
// goroutine.
func setupFileTestDB(t *testing.T) *db.Repository {
	t.Helper()
	gdb, err := dal.Open(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if s, err := gdb.DB(); err == nil {
			_ = s.Close()
		}
	})
	return db.NewRepository(gdb)
}

func TestScanProgress_FinalState(t *testing.T) {
	fs := &fakeFS{root: testTree()}
	repo := setupFileTestDB(t)
	session := &model.ScanSession{ID: 1, RootPath: "root", Status: "scanning"}
	if err := repo.DB().Create(session).Error; err != nil {
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
	session := &model.ScanSession{ID: 1, RootPath: "root", Status: "scanning"}
	if err := repo.DB().Create(session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	sc := New(gfs, repo, "", nil)

	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- sc.Scan(context.Background(), session)
	}()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gfs.release) }) }
	// A failed assertion must not leave the scan running into the next test.
	t.Cleanup(func() {
		release()
		<-finished
	})

	select {
	case <-gfs.reached:
	case err := <-done:
		t.Fatalf("scan finished without listing the gated directory: %v", err)
	}

	var row *model.ScanSession
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

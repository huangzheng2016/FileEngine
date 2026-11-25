package scanner

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// readGateFS blocks the first read of each gated file until its gate is
// released, so a test can look at the session row inside a post pass.
type readGateFS struct {
	*remotefs.LocalFS
	mu      sync.Mutex
	gates   map[string]chan struct{} // released by the test
	reached map[string]chan struct{}
}

func (g *readGateFS) ReadFile(ctx context.Context, p string) (io.ReadCloser, error) {
	g.mu.Lock()
	release, ok := g.gates[p]
	if ok {
		delete(g.gates, p)
		close(g.reached[p])
	}
	g.mu.Unlock()
	if ok {
		<-release
	}
	return g.LocalFS.ReadFile(ctx, p)
}

func TestScanPhases_ReportedPerPass(t *testing.T) {
	dirPath := t.TempDir()
	writeFile(t, dirPath, "a.jpg", []byte("same"), time.Time{})
	writeFile(t, dirPath, "b.jpg", []byte("same"), time.Time{})
	writeFile(t, dirPath, "c.txt", []byte("12345"), time.Time{})
	writeFile(t, dirPath, "d.txt", []byte("abcde"), time.Time{})
	local, err := remotefs.NewLocalFS(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	releaseMeta, releaseDup := make(chan struct{}), make(chan struct{})
	fs := &readGateFS{LocalFS: local,
		gates:   map[string]chan struct{}{"a.jpg": releaseMeta, "c.txt": releaseDup},
		reached: map[string]chan struct{}{"a.jpg": make(chan struct{}), "c.txt": make(chan struct{})},
	}
	repo := setupFileTestDB(t)
	session := &model.ScanSession{RootPath: ".", Status: "scanning", ExtractMetadata: true, DetectDuplicates: true}
	if err := repo.DB().Create(session).Error; err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- New(fs, repo, "", nil).Scan(context.Background(), session)
	}()
	var once1, once2 sync.Once
	t.Cleanup(func() {
		once1.Do(func() { close(releaseMeta) })
		once2.Do(func() { close(releaseDup) })
		<-finished
	})

	at := func(gate string, phase string, total int64) {
		t.Helper()
		select {
		case <-fs.reached[gate]:
		case err := <-done:
			t.Fatalf("scan finished before reading %s: %v", gate, err)
		}
		row, err := repo.GetSession(session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if row.ScanPhase != phase || row.PhaseTotal != total || row.PhaseDone > total {
			t.Errorf("at %s: phase %q %d/%d, want %q of %d", gate, row.ScanPhase, row.PhaseDone, row.PhaseTotal, phase, total)
		}
	}
	at("a.jpg", db.ScanPhaseMetadata, 4) // every file still lacking metadata
	once1.Do(func() { close(releaseMeta) })
	at("c.txt", db.ScanPhaseDuplicates, 18) // bytes of the files sharing a size
	once2.Do(func() { close(releaseDup) })
	if err := <-done; err != nil {
		t.Fatalf("Scan: %v", err)
	}

	row, err := repo.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "scanned" || row.ScanPhase != "" || row.PhaseDone != 0 || row.PhaseTotal != 0 {
		t.Errorf("after scan: status %q phase %q %d/%d, want scanned with no phase", row.Status, row.ScanPhase, row.PhaseDone, row.PhaseTotal)
	}
}

// A cancelled scan's last progress write clears the phase.
func TestScanPhases_ClearedOnStop(t *testing.T) {
	dirPath := t.TempDir()
	writeFile(t, dirPath, "a.jpg", []byte("same"), time.Time{})
	local, err := remotefs.NewLocalFS(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	fs := &readGateFS{LocalFS: local,
		gates:   map[string]chan struct{}{"a.jpg": release},
		reached: map[string]chan struct{}{"a.jpg": make(chan struct{})},
	}
	repo := setupFileTestDB(t)
	session := &model.ScanSession{RootPath: ".", Status: "scanning", ExtractMetadata: true}
	if err := repo.DB().Create(session).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(fs, repo, "", nil).Scan(ctx, session) }()
	<-fs.reached["a.jpg"]
	cancel()
	close(release)
	if err := <-done; err == nil {
		t.Fatal("cancelled scan returned no error")
	}
	row, err := repo.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.ScanPhase != "" || row.PhaseDone != 0 || row.PhaseTotal != 0 {
		t.Errorf("after stop: phase %q %d/%d, want cleared", row.ScanPhase, row.PhaseDone, row.PhaseTotal)
	}
}

func TestDuplicatePhase_DoneReachesTotal(t *testing.T) {
	dirPath := t.TempDir()
	big := make([]byte, 3*partialChunk)
	writeFile(t, dirPath, "x/big1.bin", big, time.Time{})
	writeFile(t, dirPath, "x/big2.bin", big, time.Time{})
	writeFile(t, dirPath, "s1.txt", []byte("same"), time.Time{})
	writeFile(t, dirPath, "s2.txt", []byte("same"), time.Time{})
	fs, err := remotefs.NewLocalFS(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := setupFileTestDB(t)
	sc, session := scanSession(t, repo, fs, ".", nil)
	sc.progress = &Progress{}
	if err := sc.detectDuplicates(context.Background(), session.ID, true); err != nil {
		t.Fatal(err)
	}
	phase, done, total := sc.progress.Phase()
	if want := int64(2*len(big) + 8); phase != db.ScanPhaseDuplicates || total != want || done != want {
		t.Errorf("phase %q %d/%d, want duplicates %d/%d", phase, done, total, want, want)
	}
}

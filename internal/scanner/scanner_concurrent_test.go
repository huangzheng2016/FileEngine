package scanner

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// memNode is one node in an in-memory filesystem tree.
type memNode struct {
	name     string
	children []*memNode
	isDir    bool
	size     int64
	mtime    time.Time
}

func dir(name string, children ...*memNode) *memNode {
	return &memNode{name: name, children: children, isDir: true}
}

func file(name string) *memNode {
	return &memNode{name: name}
}

// fakeFS serves an in-memory tree over the plain RemoteFS interface.
type fakeFS struct {
	root *memNode
	// failOn, when non-empty, makes List of any path containing it error.
	failOn string
}

func (f *fakeFS) find(p string) *memNode {
	p = strings.Trim(p, "/")
	if p == "" || p == "." || p == f.root.name {
		return f.root
	}
	segs := strings.Split(p, "/")
	if segs[0] == f.root.name {
		segs = segs[1:]
	}
	cur := f.root
	for _, seg := range segs {
		var next *memNode
		for _, c := range cur.children {
			if c.name == seg {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

func (f *fakeFS) List(_ context.Context, p string) ([]remotefs.FileInfo, error) {
	if f.failOn != "" && strings.Contains(p, f.failOn) {
		return nil, fmt.Errorf("simulated list failure on %s", p)
	}
	n := f.find(p)
	if n == nil || !n.isDir {
		return nil, fmt.Errorf("not a directory: %s", p)
	}
	var out []remotefs.FileInfo
	for _, c := range n.children {
		ft := remotefs.FileTypeFile
		if c.isDir {
			ft = remotefs.FileTypeDirectory
		}
		out = append(out, remotefs.FileInfo{
			Name: c.name, Path: joinPath(p, c.name), FileType: ft, IsDir: c.isDir,
			Size: c.size, ModTime: c.mtime,
		})
	}
	return out, nil
}

func joinPath(p, name string) string {
	if p == "" || p == "." || p == "/" {
		return name
	}
	return strings.TrimSuffix(p, "/") + "/" + name
}

func (f *fakeFS) Stat(_ context.Context, p string) (*remotefs.FileInfo, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeFS) ReadFile(_ context.Context, p string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeFS) MoveFile(_ context.Context, src, dst string) error { return nil }
func (f *fakeFS) CopyFile(_ context.Context, src, dst string) error { return nil }
func (f *fakeFS) Delete(_ context.Context, p string) error          { return nil }
func (f *fakeFS) MkdirAll(_ context.Context, p string) error        { return nil }
func (f *fakeFS) Close() error                                      { return nil }

// fakeHandleFS wraps fakeFS with a HandleLister implementation: concurrency 4,
// handles are just the node path (opaque to the scanner). It counts how many
// listings resolved via handle vs path to prove handle chaining works.
type fakeHandleFS struct {
	*fakeFS
	mu          sync.Mutex
	handleLists int
	pathLists   int
	maxInFlight int
	curInFlight int
}

func (f *fakeHandleFS) ListConcurrency() int { return 4 }

func (f *fakeHandleFS) ListByHandle(ctx context.Context, p string, handle []byte) ([]remotefs.FileInfo, error) {
	f.mu.Lock()
	f.curInFlight++
	if f.curInFlight > f.maxInFlight {
		f.maxInFlight = f.curInFlight
	}
	if len(handle) > 0 {
		f.handleLists++
	} else {
		f.pathLists++
	}
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.curInFlight--; f.mu.Unlock() }()

	// Simulate RPC latency so the worker pool genuinely overlaps and
	// cancellation can land mid-walk.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(2 * time.Millisecond):
	}

	// Handles stand in for server file handles; a real backend resolves the
	// directory through them. Here both routes list by path.
	entries, err := f.fakeFS.List(ctx, p)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].IsDir {
			entries[i].Handle = []byte(entries[i].Path)
		}
	}
	return entries, nil
}

// collectPaths scans and returns the sorted OriginalPath list plus the
// session row for assertions.
func runScan(t *testing.T, fs remotefs.RemoteFS, filterMode string, filterDirs []string) ([]string, *db.ScanSession, error) {
	t.Helper()
	repo := setupTestDB(t)
	session := &db.ScanSession{ID: 1, RootPath: "root", Status: "scanning"}
	if err := repo.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sc := New(fs, repo, filterMode, filterDirs)
	err := sc.Scan(context.Background(), session)
	files, _, qerr := repo.ListFiles(db.FileQuery{SessionID: 1, PageSize: 10000})
	if qerr != nil {
		t.Fatalf("list files: %v", qerr)
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.OriginalPath)
	}
	sort.Strings(paths)
	return paths, session, err
}

func testTree() *memNode {
	return dir("root",
		dir("photos",
			file("a.jpg"),
			file("b.jpg"),
			dir("@eaDir", file("thumb.jpg")), // junk: pruned at traversal
			dir("raw", file("c.arw"), file("d.arw")),
		),
		dir("docs",
			file("x.pdf"),
			dir("sub", file("y.pdf")),
		),
		dir("$RECYCLE.BIN", file("trash.exe")), // junk: pruned at traversal
		file("root.txt"),
		file(".DS_Store"), // junk FILE: agent layer's job, must still be listed
	)
}

func wantPaths() []string {
	return []string{
		"root/.DS_Store",
		"root/docs",
		"root/docs/sub",
		"root/docs/sub/y.pdf",
		"root/docs/x.pdf",
		"root/photos",
		"root/photos/a.jpg",
		"root/photos/b.jpg",
		"root/photos/raw",
		"root/photos/raw/c.arw",
		"root/photos/raw/d.arw",
		"root/root.txt",
	}
}

func TestScanSerial_JunkPruneAndShape(t *testing.T) {
	fs := &fakeFS{root: testTree()}
	paths, session, err := runScan(t, fs, "", nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if session.Status != "scanned" {
		t.Errorf("status = %s", session.Status)
	}
	want := wantPaths()
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("paths:\n got %v\nwant %v", paths, want)
	}
}

func TestScanConcurrent_MatchesSerial(t *testing.T) {
	serial := &fakeFS{root: testTree()}
	serialPaths, _, err := runScan(t, serial, "", nil)
	if err != nil {
		t.Fatalf("serial Scan: %v", err)
	}

	hfs := &fakeHandleFS{fakeFS: &fakeFS{root: testTree()}}
	concPaths, session, err := runScan(t, hfs, "", nil)
	if err != nil {
		t.Fatalf("concurrent Scan: %v", err)
	}
	if strings.Join(serialPaths, ",") != strings.Join(concPaths, ",") {
		t.Errorf("concurrent paths differ from serial:\n got %v\nwant %v", concPaths, serialPaths)
	}
	if session.TotalFiles != len(serialPaths) {
		t.Errorf("TotalFiles = %d, want %d", session.TotalFiles, len(serialPaths))
	}
	// Only the root listing may go by path; every child dir must chain its
	// handle (root/photos, root/photos/raw, root/docs, root/docs/sub = 4).
	hfs.mu.Lock()
	defer hfs.mu.Unlock()
	if hfs.pathLists != 1 {
		t.Errorf("path-based lists = %d, want 1 (root only)", hfs.pathLists)
	}
	if hfs.handleLists != 4 {
		t.Errorf("handle-based lists = %d, want 4", hfs.handleLists)
	}
	if hfs.maxInFlight < 2 {
		t.Errorf("maxInFlight = %d, want >= 2 (real concurrency)", hfs.maxInFlight)
	}
}

func TestScanConcurrent_DepthAndParent(t *testing.T) {
	hfs := &fakeHandleFS{fakeFS: &fakeFS{root: testTree()}}
	repo := setupTestDB(t)
	session := &db.ScanSession{ID: 1, RootPath: "root", Status: "scanning"}
	repo.CreateSession(session)
	sc := New(hfs, repo, "", nil)
	if err := sc.Scan(context.Background(), session); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	checks := map[string]struct {
		parent string
		depth  int
	}{
		// Root-level entries get ParentPath "" (serial-walk semantics).
		"root/photos":           {"", 0},
		"root/photos/raw":       {"root/photos", 1},
		"root/photos/raw/c.arw": {"root/photos/raw", 2},
		"root/root.txt":         {"", 0},
	}
	for p, want := range checks {
		f, err := repo.GetFileByPath(1, p)
		if err != nil {
			t.Errorf("GetFileByPath(%s): %v", p, err)
			continue
		}
		if f.ParentPath != want.parent || f.Depth != want.depth {
			t.Errorf("%s: parent=%q depth=%d, want parent=%q depth=%d",
				p, f.ParentPath, f.Depth, want.parent, want.depth)
		}
	}
}

func TestScanConcurrent_ErrorAborts(t *testing.T) {
	hfs := &fakeHandleFS{fakeFS: &fakeFS{root: testTree(), failOn: "photos"}}
	_, session, err := runScan(t, hfs, "", nil)
	if err == nil {
		t.Fatal("expected scan error when a directory cannot be listed")
	}
	if !strings.Contains(err.Error(), "simulated list failure") {
		t.Errorf("error = %v, want the underlying list failure", err)
	}
	if session.Status == "scanned" {
		t.Error("session must not be marked scanned after a listing failure")
	}
}

func TestScanConcurrent_BlacklistStillApplies(t *testing.T) {
	hfs := &fakeHandleFS{fakeFS: &fakeFS{root: testTree()}}
	paths, _, err := runScan(t, hfs, "blacklist", []string{"root/docs"})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, p := range paths {
		if strings.HasPrefix(p, "root/docs") {
			t.Errorf("blacklisted path %s present in results", p)
		}
	}
}

func TestScanConcurrent_WideTreeNoDeadlock(t *testing.T) {
	// Root with far more child directories than any bounded task channel
	// could hold: workers must keep draining each other's enqueue bursts.
	children := make([]*memNode, 0, 301)
	for i := 0; i < 300; i++ {
		children = append(children, dir(fmt.Sprintf("d%03d", i), file("a"), file("b"), file("c")))
	}
	children = append(children, file("root.txt"))
	hfs := &fakeHandleFS{fakeFS: &fakeFS{root: &memNode{name: "root", isDir: true, children: children}}}

	done := make(chan error, 1)
	repo := setupTestDB(t)
	session := &db.ScanSession{ID: 1, RootPath: "root", Status: "scanning"}
	repo.CreateSession(session)
	sc := New(hfs, repo, "", nil)
	go func() { done <- sc.Scan(context.Background(), session) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("scan did not finish in 30s — work distribution deadlocked")
	}
	if session.TotalFiles != 300*4+1 {
		t.Errorf("TotalFiles = %d, want %d", session.TotalFiles, 300*4+1)
	}
}

func TestScanConcurrent_CancelStops(t *testing.T) {
	// A tree deep enough that cancellation mid-walk is observable.
	deep := dir("root")
	cur := deep
	for i := 0; i < 50; i++ {
		next := dir(fmt.Sprintf("d%02d", i), file("f"))
		cur.children = append(cur.children, next)
		cur = next
	}
	hfs := &fakeHandleFS{fakeFS: &fakeFS{root: deep}}
	repo := setupTestDB(t)
	session := &db.ScanSession{ID: 1, RootPath: "root", Status: "scanning"}
	repo.CreateSession(session)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	sc := New(hfs, repo, "", nil)
	err := sc.Scan(ctx, session)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Errorf("err = %v, want context canceled", err)
	}
}

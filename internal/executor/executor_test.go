package executor

import (
	"context"
	"errors"
	"os"
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

// faultFS wraps a LocalFS with injectable failures.
type faultFS struct {
	remotefs.RemoteFS
	mu        sync.Mutex
	noRename  bool                  // MoveFile fails like a cross-device rename
	failCopy  func(src string) bool // CopyFile fails for matching sources
	afterCopy func(src, dst string) // hook after a successful copy
	deleted   []string
}

func (f *faultFS) MoveFile(ctx context.Context, src, dst string) error {
	if f.noRename {
		return errors.New("rename: cross-device link")
	}
	return f.RemoteFS.MoveFile(ctx, src, dst)
}

func (f *faultFS) CopyFile(ctx context.Context, src, dst string) error {
	if f.failCopy != nil && f.failCopy(src) {
		return errors.New("injected copy failure")
	}
	if err := f.RemoteFS.CopyFile(ctx, src, dst); err != nil {
		return err
	}
	if f.afterCopy != nil {
		f.afterCopy(src, dst)
	}
	return nil
}

func (f *faultFS) Delete(ctx context.Context, p string) error {
	f.mu.Lock()
	f.deleted = append(f.deleted, p)
	f.mu.Unlock()
	return f.RemoteFS.Delete(ctx, p)
}

type env struct {
	t    *testing.T
	root string
	repo *db.Repository
	fs   *faultFS
	sid  uint
}

func newEnv(t *testing.T) *env {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := gdb.AutoMigrate(&db.FileEntry{}, &db.ScanSession{}, &db.AgentRun{}, &db.ExecOp{}); err != nil {
		t.Fatal(err)
	}
	repo := db.NewRepository(gdb)
	s := &db.ScanSession{Status: "scanned"}
	if err := repo.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	lfs, err := remotefs.NewLocalFS(root)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, root: root, repo: repo, fs: &faultFS{RemoteFS: lfs}, sid: s.ID}
}

func (e *env) write(p, data string) {
	e.t.Helper()
	full := filepath.Join(e.root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) read(p string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(e.root, p))
	return string(b), err == nil
}

func (e *env) exists(p string) bool {
	_, err := os.Lstat(filepath.Join(e.root, p))
	return err == nil
}

// entry records p as scanned (live size/mtime) and, when target != "", decided.
func (e *env) entry(p, target string) *db.FileEntry {
	e.t.Helper()
	st, err := os.Stat(filepath.Join(e.root, p))
	if err != nil {
		e.t.Fatal(err)
	}
	ft := "file"
	if st.IsDir() {
		ft = "directory"
	}
	f := &db.FileEntry{ScanSessionID: e.sid, OriginalPath: p, Name: filepath.Base(p), FileType: ft,
		Size: st.Size(), ModTime: st.ModTime(), ParentPath: filepath.Dir(p)}
	if target != "" {
		f.NewPath, f.Operation = "/"+target, "planned"
	}
	if err := e.repo.CreateFile(f); err != nil {
		e.t.Fatal(err)
	}
	return f
}

func (e *env) run(ctx context.Context, opts Options) (*Result, error) {
	e.t.Helper()
	run, err := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "test")
	if err != nil {
		e.t.Fatal(err)
	}
	res, err := New(e.repo, e.fs).Execute(ctx, run, opts)
	e.repo.FinishRun(run, db.RunDone, "", "", db.TokenCounts{})
	return res, err
}

func (e *env) executed(id uint) bool {
	f, err := e.repo.GetFile(id)
	if err != nil {
		e.t.Fatal(err)
	}
	return f.Executed
}

func hasIssue(res *Result, code string) bool {
	for _, is := range res.Issues {
		if is.Code == code {
			return true
		}
	}
	return false
}

func TestMoveFilesAndDirByRename(t *testing.T) {
	e := newEnv(t)
	e.write("in/a.txt", "A")
	e.write("proj/src/main.go", "package main")
	fa := e.entry("in/a.txt", "docs/a.txt")
	fp := e.entry("proj", "code/proj")
	res, err := e.run(context.Background(), Options{Mode: ModeMove})
	if err != nil || res.Success != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if s, _ := e.read("docs/a.txt"); s != "A" || e.exists("in/a.txt") {
		t.Fatal("file not moved")
	}
	if s, _ := e.read("code/proj/src/main.go"); s != "package main" || e.exists("proj") {
		t.Fatal("dir not moved")
	}
	if !e.executed(fa.ID) || !e.executed(fp.ID) {
		t.Fatal("entries not marked executed")
	}
}

func TestCopyFailureMidDirNeverDeletesSource(t *testing.T) {
	e := newEnv(t)
	e.write("d/1.txt", "one")
	e.write("d/2.txt", "two")
	e.write("d/3.txt", "three")
	f := e.entry("d", "out/d")
	e.fs.noRename = true
	e.fs.failCopy = func(src string) bool { return src == "d/2.txt" }
	res, err := e.run(context.Background(), Options{Mode: ModeMove})
	if err != nil || res.Failed != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if s, ok := e.read("d/2.txt"); !ok || s != "two" {
		t.Fatal("failed file's source must stay")
	}
	if !e.exists("d") {
		t.Fatal("source directory must not be removed while not empty")
	}
	for _, p := range e.fs.deleted {
		if p == "d/2.txt" || p == "d" {
			t.Fatalf("deleted %s", p)
		}
	}
	if s, _ := e.read("out/d/1.txt"); s != "one" || e.exists("d/1.txt") {
		t.Fatal("verified files should be moved")
	}
	if e.executed(f.ID) {
		t.Fatal("partially moved dir must not be marked executed")
	}

	// Next run resumes into the dir it already created.
	e.fs.failCopy = nil
	res, err = e.run(context.Background(), Options{Mode: ModeMove})
	if err != nil || res.Success != 1 {
		t.Fatalf("resume res=%+v err=%v", res, err)
	}
	if s, _ := e.read("out/d/2.txt"); s != "two" || e.exists("d") || !e.executed(f.ID) {
		t.Fatal("resume did not finish the directory")
	}
}

func TestCancelMidMoveKeepsSources(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "A")
	e.write("b.txt", "B")
	e.entry("a.txt", "x/a.txt")
	e.entry("b.txt", "x/b.txt")
	e.fs.noRename = true
	ctx, cancel := context.WithCancel(context.Background())
	e.fs.afterCopy = func(src, dst string) { cancel() }
	_, err := e.run(ctx, Options{Mode: ModeMove})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
	if !e.exists("a.txt") || !e.exists("b.txt") {
		t.Fatal("cancel must never delete a source")
	}
	if len(e.fs.deleted) != 0 {
		t.Fatalf("deletes after cancel: %v", e.fs.deleted)
	}

	// The next run finishes the verified copy first, then the rest.
	e.fs.afterCopy = nil
	res, err := e.run(context.Background(), Options{Mode: ModeMove})
	if err != nil {
		t.Fatal(err)
	}
	if e.exists("a.txt") || e.exists("b.txt") || !e.exists("x/a.txt") || !e.exists("x/b.txt") {
		t.Fatalf("resume incomplete: %+v", res)
	}
}

func TestTargetExistsSkipped(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "new")
	e.write("dst/A.TXT", "old")
	e.entry("a.txt", "dst/a.txt")
	res, err := e.run(context.Background(), Options{Mode: ModeMove})
	if err != nil || res.Skipped != 1 || !hasIssue(res, IssueTargetExists) {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if s, _ := e.read("dst/A.TXT"); s != "old" {
		t.Fatal("existing target modified")
	}
	if s, _ := e.read("a.txt"); s != "new" {
		t.Fatal("source modified")
	}
}

func TestDirIntoOwnSubtreeRejected(t *testing.T) {
	e := newEnv(t)
	e.write("a/f.txt", "x")
	e.entry("a", "a/b")
	res, _ := e.run(context.Background(), Options{Mode: ModeMove})
	if !hasIssue(res, IssueIntoSelf) || res.Success != 0 || !e.exists("a/f.txt") {
		t.Fatalf("res=%+v", res)
	}
}

func TestNestedEntrySkipped(t *testing.T) {
	e := newEnv(t)
	e.write("p/f.txt", "x")
	e.entry("p", "out/p")
	e.entry("p/f.txt", "other/f.txt")
	res, _ := e.run(context.Background(), Options{Mode: ModeMove})
	if !hasIssue(res, IssueNested) || res.Success != 1 || e.exists("other/f.txt") {
		t.Fatalf("res=%+v", res)
	}
}

func TestStaleSourceSkipped(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "A")
	e.entry("a.txt", "out/a.txt")
	e.write("a.txt", "changed after scan")
	res, _ := e.run(context.Background(), Options{Mode: ModeMove})
	if !hasIssue(res, IssueSourceChanged) || e.exists("out/a.txt") {
		t.Fatalf("res=%+v", res)
	}
}

func TestCaseAndNFCCollision(t *testing.T) {
	e := newEnv(t)
	e.write("1.txt", "1")
	e.write("2.txt", "2")
	e.entry("1.txt", "Out/Café.txt")
	e.entry("2.txt", "out/café.TXT")
	res, _ := e.run(context.Background(), Options{Mode: ModeCopy})
	if res.Success != 1 || !hasIssue(res, IssueCollision) {
		t.Fatalf("res=%+v", res)
	}
}

func TestJournalResumeAfterCrash(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "AAAA")
	e.write("b.txt", "BBBB")
	fa := e.entry("a.txt", "x/a.txt")
	fb := e.entry("b.txt", "x/b.txt")
	run, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "crashed")
	st := func(p string) (int64, time.Time) {
		s, _ := os.Stat(filepath.Join(e.root, p))
		return s.Size(), s.ModTime()
	}
	// a: crashed mid-copy, partial dst left behind.
	e.write("x/a.txt", "AA")
	sa, ma := st("a.txt")
	e.repo.CreateExecOp(&db.ExecOp{RunID: run.ID, SessionID: e.sid, EntryID: fa.ID, Kind: db.ExecKindMove,
		Src: "a.txt", Dst: "x/a.txt", Size: sa, ModTime: ma, State: db.ExecPending})
	// b: copy verified, crashed before deleting the source.
	e.write("x/b.txt", "BBBB")
	sb, mb := st("b.txt")
	e.repo.CreateExecOp(&db.ExecOp{RunID: run.ID, SessionID: e.sid, EntryID: fb.ID, Kind: db.ExecKindMove,
		Src: "b.txt", Dst: "x/b.txt", Size: sb, ModTime: mb, State: db.ExecVerified})
	e.repo.FinishRun(run, db.RunStopped, "", "", db.TokenCounts{})

	if err := New(e.repo, e.fs).Recover(context.Background(), e.sid); err != nil {
		t.Fatal(err)
	}
	if e.exists("x/a.txt") || !e.exists("a.txt") {
		t.Fatal("partial copy must be removed, source kept")
	}
	if e.exists("b.txt") || !e.executed(fb.ID) {
		t.Fatal("verified move must be completed")
	}
	res, err := e.run(context.Background(), Options{Mode: ModeMove})
	if err != nil || res.Success != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if s, _ := e.read("x/a.txt"); s != "AAAA" || !e.executed(fa.ID) {
		t.Fatal("remaining entry not executed")
	}
}

func TestRevertRestores(t *testing.T) {
	e := newEnv(t)
	e.write("in/a.txt", "A")
	e.write("in/b.txt", "B")
	e.write("proj/x.go", "x")
	fa := e.entry("in/a.txt", "docs/new/a.txt")
	e.entry("in/b.txt", "docs/new/b.txt")
	fp := e.entry("proj", "code/proj")
	runRes, err := e.run(context.Background(), Options{Mode: ModeMove})
	if err != nil || runRes.Success != 3 {
		t.Fatalf("%+v %v", runRes, err)
	}
	// A user edit after execution blocks reverting that one file.
	e.write("docs/new/b.txt", "edited")

	var target db.AgentRun
	e.repo.DB().Order("id DESC").First(&target)
	rev, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "revert")
	res, err := New(e.repo, e.fs).Revert(context.Background(), rev, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := e.read("in/a.txt"); s != "A" || e.exists("docs/new/a.txt") {
		t.Fatal("a not restored")
	}
	if !e.exists("proj/x.go") || e.exists("code/proj") || e.exists("code") {
		t.Fatal("dir not restored or created dirs left")
	}
	if s, _ := e.read("docs/new/b.txt"); s != "edited" || e.exists("in/b.txt") || !hasIssue(res, IssueSourceChanged) {
		t.Fatalf("modified target must stay: %+v", res)
	}
	if e.executed(fa.ID) || e.executed(fp.ID) {
		t.Fatal("reverted entries must be unmarked")
	}
}

func TestRevertCopyRun(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "A")
	e.entry("a.txt", "c/a.txt")
	e.fs.noRename = true
	if _, err := e.run(context.Background(), Options{Mode: ModeCopy}); err != nil {
		t.Fatal(err)
	}
	var target db.AgentRun
	e.repo.DB().Order("id DESC").First(&target)
	rev, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "revert")
	if _, err := New(e.repo, e.fs).Revert(context.Background(), rev, target.ID); err != nil {
		t.Fatal(err)
	}
	if e.exists("c") || !e.exists("a.txt") {
		t.Fatal("copy revert must remove the copy and its created dir only")
	}
}

func TestRevertRemovesOnlyEmptyCreatedDirs(t *testing.T) {
	e := newEnv(t)
	e.write("proj/src/deep/main.go", "x")
	e.write("in/a.txt", "a")
	e.write("in/b.txt", "b")
	e.entry("proj", "x/y/proj")
	e.entry("in/a.txt", "p/q/a.txt")
	e.entry("in/b.txt", "k/b.txt")
	e.fs.noRename = true
	if res, err := e.run(context.Background(), Options{Mode: ModeMove}); err != nil || res.Success != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	// A file the user added after execution keeps its directory.
	e.write("k/user.txt", "mine")

	var target db.AgentRun
	e.repo.DB().Order("id DESC").First(&target)
	rev, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "revert")
	res, err := New(e.repo, e.fs).Revert(context.Background(), rev, target.ID)
	if err != nil || res.Failed != 0 || res.Skipped != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, p := range []string{"x", "p"} {
		if e.exists(p) {
			t.Fatalf("created dir %s left behind", p)
		}
	}
	if !e.exists("proj/src/deep/main.go") || !e.exists("in/a.txt") || !e.exists("in/b.txt") {
		t.Fatal("sources not restored")
	}
	if s, _ := e.read("k/user.txt"); s != "mine" {
		t.Fatal("non-empty created dir must be kept")
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "目录「k」非空，已保留") {
		t.Fatalf("notes: %q", res.Notes)
	}
	// Reverting again settles nothing new and never touches the kept dir.
	e.repo.FinishRun(rev, db.RunDone, "", "", db.TokenCounts{})
	rev2, _ := e.repo.CreateRun(e.sid, db.RunPhaseExecuting, "revert")
	if _, err := New(e.repo, e.fs).Revert(context.Background(), rev2, target.ID); err != nil {
		t.Fatal(err)
	}
	if !e.exists("k/user.txt") {
		t.Fatal("kept dir touched")
	}
}

func TestSampleAndCategoryExecution(t *testing.T) {
	e := newEnv(t)
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		e.write(n+".txt", n)
		cat := "a"
		if n == "5" {
			cat = "b"
		}
		e.entry(n+".txt", cat+"/"+n+".txt")
	}
	res, err := e.run(context.Background(), Options{Mode: ModeCopy, Sample: 2})
	if err != nil || res.Success != 2 || res.Total != 2 {
		t.Fatalf("sample: %+v %v", res, err)
	}
	res, err = e.run(context.Background(), Options{Mode: ModeCopy, CategoryPath: "/b"})
	if err != nil || res.Success != 1 || !e.exists("b/5.txt") || e.exists("a/3.txt") {
		t.Fatalf("category: %+v %v", res, err)
	}
}

func TestValidateDoesNotTouchFS(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "A")
	e.write("dst/a.txt", "old")
	e.entry("a.txt", "dst/a.txt")
	res, err := New(e.repo, e.fs).Validate(context.Background(), e.sid, Options{Mode: ModeMove})
	if err != nil || !hasIssue(res, IssueTargetExists) || res.Skipped != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestLocate(t *testing.T) {
	e := newEnv(t)
	e.write("proj/sub/readme.md", "x")
	e.entry("proj", "code/proj")
	e.entry("proj/sub/readme.md", "")
	if _, err := e.run(context.Background(), Options{Mode: ModeMove}); err != nil {
		t.Fatal(err)
	}
	items, err := Locate(e.repo, e.sid, "README", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("%+v %v", items, err)
	}
	it := items[0]
	if it.NewPath != "/code/proj/sub/readme.md" || it.DecidedBy != "proj" || !it.Executed || it.OpState != db.ExecDone {
		t.Fatalf("%+v", it)
	}
	if !strings.HasSuffix(it.OriginalPath, "readme.md") {
		t.Fatal(it.OriginalPath)
	}
}

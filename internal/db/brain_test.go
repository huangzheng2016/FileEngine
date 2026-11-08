package db

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"FileEngine/internal/config"
)

func setupBrainRepo(t testing.TB) *Repository {
	t.Helper()
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "test.db")}); err != nil {
		t.Fatalf("init: %v", err)
	}
	return NewRepository(DB)
}

func TestPlanLifecycle(t *testing.T) {
	r := setupBrainRepo(t)

	p, err := r.GetPlan(1)
	if err != nil || p.Version != 0 || p.SessionID != 1 {
		t.Fatalf("missing plan must read as version 0: %+v %v", p, err)
	}
	if has, _ := r.HasPlan(1); has {
		t.Fatal("HasPlan on empty")
	}
	if _, err := r.ApprovePlan(1); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("approve without plan: %v", err)
	}
	if _, err := r.SavePlan(1, "x", 3, ""); !errors.Is(err, ErrPlanVersionConflict) {
		t.Fatalf("creating with a non-zero version must conflict: %v", err)
	}

	p, err = r.SavePlan(1, "# 方案 v1", 0, "")
	if err != nil || p.Version != 1 || p.Status != PlanDraft || p.Content != "# 方案 v1" {
		t.Fatalf("create: %+v %v", p, err)
	}
	if _, err := r.SavePlan(1, "stale", 0, ""); !errors.Is(err, ErrPlanVersionConflict) {
		t.Fatalf("stale version must conflict: %v", err)
	}
	p, err = r.ApprovePlan(1)
	if err != nil || p.Status != PlanApproved || p.ApprovedAt == nil || p.Version != 1 {
		t.Fatalf("approve: %+v %v", p, err)
	}
	p, err = r.SavePlan(1, "# 方案 v2", 1, "")
	if err != nil || p.Version != 2 || p.Status != PlanApproved {
		t.Fatalf("user edit: %+v %v", p, err)
	}
	p, err = r.SavePlan(1, "# 方案 v3", -1, PlanDraft)
	if err != nil || p.Version != 3 || p.Status != PlanDraft {
		t.Fatalf("agent write: %+v %v", p, err)
	}
	if has, _ := r.HasPlan(1); !has {
		t.Fatal("HasPlan after save")
	}

	if err := r.DeleteSessionBrainData(1); err != nil {
		t.Fatal(err)
	}
	if p, _ := r.GetPlan(1); p.Version != 0 {
		t.Fatalf("plan must be gone: %+v", p)
	}
}

func TestMemoryUpsertValidateAndList(t *testing.T) {
	r := setupBrainRepo(t)

	m := &MemoryItem{Scope: MemoryScopeFilesystem, ScopeID: 2, Kind: MemoryConvention, Key: "  EXT:.PSD ", Content: "psd 放设计", Source: MemorySourceAgent, Pinned: true}
	created, err := r.UpsertMemory(m)
	if err != nil || !created || m.ID == 0 || m.Key != "ext:.psd" {
		t.Fatalf("create: %+v %v %v", m, created, err)
	}
	first := m.ID

	m2 := &MemoryItem{Scope: MemoryScopeFilesystem, ScopeID: 2, Kind: MemoryCorrection, Key: "ext:.psd", Content: "psd 放 /设计/源文件", Path: "/Design/", Source: MemorySourceCorrection}
	created, err = r.UpsertMemory(m2)
	if err != nil || created || m2.ID != first || !m2.Pinned || m2.Content != "psd 放 /设计/源文件" || m2.Path != "Design" {
		t.Fatalf("update: %+v %v %v", m2, created, err)
	}

	for _, bad := range []*MemoryItem{
		{Scope: "team", ScopeID: 1, Kind: MemoryFact, Key: "k", Content: "c"},
		{Scope: MemoryScopeGlobal, ScopeID: 3, Kind: MemoryFact, Key: "k", Content: "c"},
		{Scope: MemoryScopeFilesystem, Kind: MemoryFact, Key: "k", Content: "c"},
		{Scope: MemoryScopeGlobal, Kind: "opinion", Key: "k", Content: "c"},
		{Scope: MemoryScopeGlobal, Kind: MemoryFact, Key: " ", Content: "c"},
		{Scope: MemoryScopeGlobal, Kind: MemoryFact, Key: "k", Content: strings.Repeat("长", 301)},
	} {
		_, err := r.UpsertMemory(bad)
		var v *MemoryValidationError
		if !errors.As(err, &v) {
			t.Fatalf("invalid item %+v must be a validation error, got %v", bad, err)
		}
	}

	g := &MemoryItem{Scope: MemoryScopeGlobal, Kind: MemoryPreference, Key: "pref:lang", Content: "文件夹用中文名", Source: MemorySourceUser}
	if _, err := r.UpsertMemory(g); err != nil {
		t.Fatal(err)
	}
	other := &MemoryItem{Scope: MemoryScopeFilesystem, ScopeID: 9, Kind: MemoryFact, Key: "x", Content: "别的文件系统"}
	if _, err := r.UpsertMemory(other); err != nil {
		t.Fatal(err)
	}
	lt, err := r.ListLongTermMemory(2)
	if err != nil || len(lt) != 2 {
		t.Fatalf("long-term for fs 2 must be global + fs2 only: %+v %v", lt, err)
	}
	if lt[0].ID != first {
		t.Fatalf("pinned item must rank first: %+v", lt)
	}
	all, _ := r.ListMemory("", nil)
	if len(all) != 3 {
		t.Fatalf("list all: %d", len(all))
	}

	if _, err := r.UpsertMemory(&MemoryItem{Scope: MemoryScopeFilesystem, ScopeID: 2, Kind: MemoryFact, Key: "other", Content: "c"}); err != nil {
		t.Fatal(err)
	}
	_, err = r.UpdateMemory(first, func(m *MemoryItem) { m.Key = "OTHER" })
	var v *MemoryValidationError
	if !errors.As(err, &v) {
		t.Fatalf("key clash must be a validation error: %v", err)
	}
	upd, err := r.UpdateMemory(first, func(m *MemoryItem) { m.Pinned = false; m.Content = "新内容" })
	if err != nil || upd.Pinned || upd.Content != "新内容" {
		t.Fatalf("update: %+v %v", upd, err)
	}
	if _, err := r.UpdateMemory(9999, func(m *MemoryItem) {}); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	if err := r.DeleteMemory(first); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteMemory(first); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("double delete: %v", err)
	}
}

func TestMemorySearchEscapesAndBumps(t *testing.T) {
	r := setupBrainRepo(t)
	items := []*MemoryItem{
		{Scope: MemoryScopeFilesystem, ScopeID: 1, Kind: MemoryConvention, Key: "ext:.PSD", Content: "设计源文件放 /设计"},
		{Scope: MemoryScopeFilesystem, ScopeID: 1, Kind: MemoryFact, Key: "quota", Content: "占用 50% 空间"},
		{Scope: MemoryScopeFilesystem, ScopeID: 1, Kind: MemoryFact, Key: "quota2", Content: "占用 500 空间"},
		{Scope: MemoryScopeFilesystem, ScopeID: 2, Kind: MemoryFact, Key: "psd", Content: "别的文件系统的 psd"},
	}
	for _, m := range items {
		if _, err := r.UpsertMemory(m); err != nil {
			t.Fatal(err)
		}
	}
	scopes := []MemoryScopeRef{{Scope: MemoryScopeFilesystem, ScopeID: 1}}

	got, err := r.SearchMemory(MemorySearch{Query: "psd", Scopes: scopes})
	if err != nil || len(got) != 1 || got[0].Key != "ext:.psd" {
		t.Fatalf("case-insensitive key match scoped to fs 1: %+v %v", got, err)
	}
	got, _ = r.SearchMemory(MemorySearch{Query: "50%", Scopes: scopes})
	if len(got) != 1 || got[0].Key != "quota" {
		t.Fatalf("%% must match literally: %+v", got)
	}
	got, _ = r.SearchMemory(MemorySearch{Query: "占用 空间", Scopes: scopes})
	if len(got) != 2 {
		t.Fatalf("all terms must match: %+v", got)
	}
	if got, _ := r.SearchMemory(MemorySearch{Query: "psd"}); len(got) != 0 {
		t.Fatal("no scopes means no results")
	}

	if err := r.BumpMemoryHits([]uint{items[0].ID}); err != nil {
		t.Fatal(err)
	}
	m, _ := r.GetMemory(items[0].ID)
	if m.Hits != 1 || m.LastUsedAt == nil {
		t.Fatalf("bump: %+v", m)
	}
}

func TestNotesSince(t *testing.T) {
	r := setupBrainRepo(t)
	var ids []uint
	for _, c := range []string{"a 是项目根", "b 用年份命名", "c 是照片"} {
		m := &MemoryItem{Scope: MemoryScopeSession, ScopeID: 5, Kind: MemoryNote, Key: "note:" + c, Content: c}
		if _, err := r.UpsertMemory(m); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if _, err := r.UpsertMemory(&MemoryItem{Scope: MemoryScopeSession, ScopeID: 6, Kind: MemoryNote, Key: "note:x", Content: "其他会话"}); err != nil {
		t.Fatal(err)
	}
	got, err := r.NotesSince(5, ids[0], 0)
	if err != nil || len(got) != 2 || got[0].ID != ids[1] || got[1].ID != ids[2] {
		t.Fatalf("notes after cursor, oldest first, same session: %+v %v", got, err)
	}
	if err := r.DeleteSessionBrainData(5); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.NotesSince(5, 0, 0); len(got) != 0 {
		t.Fatal("session notes must be deleted with the session")
	}
	if got, _ := r.NotesSince(6, 0, 0); len(got) != 1 {
		t.Fatal("other session's notes must survive")
	}
}

func TestCollisionName(t *testing.T) {
	cases := map[string]string{
		"/a/b.txt":     "/a/b (2).txt",
		"/a/b":         "/a/b (2)",
		"/a/x.tar.gz":  "/a/x (2).tar.gz",
		"/a/.bashrc":   "/a/.bashrc (2)",
		"/a/c.d/e.JPG": "/a/c.d/e (2).JPG",
	}
	for in, want := range cases {
		if got := CollisionName(in, 2); got != want {
			t.Fatalf("CollisionName(%q) = %q, want %q", in, got, want)
		}
	}
	if NormalizeTargetPath("a/b/../c") != "/a/c" || NormalizeTargetPath("") != "" || NormalizeTargetPath("/x/") != "/x" {
		t.Fatal("NormalizeTargetPath")
	}
}

func seedEntry(t *testing.T, r *Repository, sid uint, p, typ string, size int64) *FileEntry {
	t.Helper()
	parent := ""
	if i := strings.LastIndex(p, "/"); i >= 0 {
		parent = p[:i]
	}
	f := &FileEntry{ScanSessionID: sid, OriginalPath: p, Name: p[strings.LastIndex(p, "/")+1:], FileType: typ, ParentPath: parent, Size: size, ModTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := r.CreateFile(f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSetTargetUniqueResolvesCollisions(t *testing.T) {
	r := setupBrainRepo(t)
	seedEntry(t, r, 1, "a/x.jpg", "file", 1)
	seedEntry(t, r, 1, "b/x.jpg", "file", 1)
	seedEntry(t, r, 1, "c/x.jpg", "file", 1)
	seedEntry(t, r, 2, "a/x.jpg", "file", 1) // other session: never collides

	got := []string{}
	for _, p := range []string{"a/x.jpg", "b/x.jpg", "c/x.jpg"} {
		final, _, err := r.SetTargetUnique(1, p, "照片/x.jpg", "file")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, final)
	}
	want := []string{"/照片/x.jpg", "/照片/x (2).jpg", "/照片/x (3).jpg"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("collision naming: got %v want %v", got, want)
		}
	}
	if final, _, _ := r.SetTargetUnique(1, "a/x.jpg", "/照片/x.jpg", "file"); final != "/照片/x.jpg" {
		t.Fatalf("self re-target: %s", final)
	}
	if final, _, _ := r.SetTargetUnique(2, "a/x.jpg", "/照片/x.jpg", "file"); final != "/照片/x.jpg" {
		t.Fatalf("other session must not collide: %s", final)
	}
	if final, _, _ := r.SetTargetUnique(1, "a/x.jpg", "", "file"); final != "" {
		t.Fatal("clear")
	}
	f, _ := r.GetFileByPath(1, "a/x.jpg")
	if f.NewPath != "" || f.Operation != "" {
		t.Fatalf("cleared row: %+v", f)
	}
}

func TestResetPlansUntagsDirectorySubtree(t *testing.T) {
	r := setupBrainRepo(t)
	d := seedEntry(t, r, 1, "proj", "directory", 0)
	seedEntry(t, r, 1, "proj/main.go", "file", 1)
	f := seedEntry(t, r, 1, "loose.txt", "file", 1)
	if _, _, err := r.SetTargetUnique(1, "proj", "/代码/proj", "directory"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetTargetUnique(1, "loose.txt", "/文档/loose.txt", "file"); err != nil {
		t.Fatal(err)
	}
	n, err := r.ResetPlans(1, []uint{d.ID, 9999})
	if err != nil || n != 1 {
		t.Fatalf("reset: %d %v", n, err)
	}
	for _, p := range []string{"proj", "proj/main.go"} {
		e, _ := r.GetFileByPath(1, p)
		if e.Tagged || e.NewPath != "" {
			t.Fatalf("%s must be reset: %+v", p, e)
		}
	}
	e, _ := r.GetFile(f.ID)
	if e.NewPath != "/文档/loose.txt" {
		t.Fatal("unrelated entry must keep its target")
	}
	if n, _ := r.ResetPlans(2, []uint{f.ID}); n != 0 {
		t.Fatal("ids of another session must be ignored")
	}
}

func TestSetTargetUniqueCaseAndNFCInsensitive(t *testing.T) {
	r := setupBrainRepo(t)
	seedEntry(t, r, 1, "a/1", "file", 1)
	seedEntry(t, r, 1, "a/2", "file", 1)
	seedEntry(t, r, 1, "a/3", "file", 1)
	first, _, _ := r.SetTargetUnique(1, "a/1", "Docs/Café.txt", "file")
	second, _, _ := r.SetTargetUnique(1, "a/2", "docs/café.TXT", "file")
	third, _, _ := r.SetTargetUnique(1, "a/3", "DOCS/CAFÉ.txt", "file")
	if first != "/Docs/Café.txt" || second == "/docs/café.TXT" {
		t.Fatalf("NFD/case variant must be renamed: %q %q", first, second)
	}
	if CollisionKey(first) == CollisionKey(second) || CollisionKey(third) == CollisionKey(first) || CollisionKey(third) == CollisionKey(second) {
		t.Fatalf("keys still collide: %q %q %q", first, second, third)
	}
}

func TestCollisionKey(t *testing.T) {
	if CollisionKey("/A/Café.TXT") != CollisionKey("a/café.txt") {
		t.Fatal("case/NFC variants must share a key")
	}
	if CollisionKey("a/b") == CollisionKey("a/c") {
		t.Fatal("distinct paths must differ")
	}
}

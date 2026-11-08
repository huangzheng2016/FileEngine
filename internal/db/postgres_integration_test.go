package db

import (
	"os"
	"testing"

	"FileEngine/internal/config"
)

// PostgreSQL integration test — environment-dependent, like the remotefs NAS
// tests, but skipped (not failed) when the environment is absent.
//
// Run against a scratch server, e.g.:
//
//	docker run --rm -d --name fe-pg -e POSTGRES_PASSWORD=test -p 5432:5432 postgres:16
//	FILEENGINE_TEST_PG_DSN="host=localhost port=5432 user=postgres password=test dbname=postgres sslmode=disable" \
//	    go test ./internal/db/ -run TestPostgres -v
//
// The test drops and recreates all tables, so never point it at a real database.
func postgresTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("FILEENGINE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("FILEENGINE_TEST_PG_DSN not set — skipping PostgreSQL integration test")
	}
	return dsn
}

func TestPostgresDialectSemantics(t *testing.T) {
	dsn := postgresTestDSN(t)
	if err := Init(config.DatabaseConfig{Driver: "postgres", DSN: dsn}); err != nil {
		t.Fatalf("init postgres: %v", err)
	}
	if DB.Dialector.Name() != "postgres" {
		t.Fatalf("dialect selection: got %q, want postgres", DB.Dialector.Name())
	}

	// Fresh slate: drop + re-migrate so the test is re-runnable on the same DB.
	resetSchema(t)
	r := NewRepository(DB)

	fs := &Filesystem{Name: "pgfs", Protocol: "local", BasePath: "/data"}
	if err := r.CreateFilesystem(fs); err != nil {
		t.Fatalf("create filesystem: %v", err)
	}
	session := &ScanSession{FilesystemID: fs.ID, Status: "tagging"}
	if err := r.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	entries := []FileEntry{
		{ScanSessionID: session.ID, OriginalPath: "Docs", Name: "Docs", FileType: "directory"},
		{ScanSessionID: session.ID, OriginalPath: "Docs/Report.PDF", Name: "Report.PDF", FileType: "file"},
		{ScanSessionID: session.ID, OriginalPath: "docs", Name: "docs", FileType: "directory"},
		{ScanSessionID: session.ID, OriginalPath: "docs/notes.txt", Name: "notes.txt", FileType: "file"},
		{ScanSessionID: session.ID, OriginalPath: "my_dir", Name: "my_dir", FileType: "directory"},
		{ScanSessionID: session.ID, OriginalPath: "my_dir/a.txt", Name: "a.txt", FileType: "file"},
		{ScanSessionID: session.ID, OriginalPath: "myXdir", Name: "myXdir", FileType: "directory"},
		{ScanSessionID: session.ID, OriginalPath: "myXdir/b.txt", Name: "b.txt", FileType: "file"},
	}
	for _, f := range entries {
		fe := f
		if err := r.CreateFile(&fe); err != nil {
			t.Fatalf("create file %s: %v", f.OriginalPath, err)
		}
	}

	// Name search must be case-insensitive on PostgreSQL too (ILIKE).
	files, total, err := r.ListFiles(FileQuery{SessionID: session.ID, Search: "report"})
	if err != nil {
		t.Fatalf("search report: %v", err)
	}
	if total != 1 || len(files) != 1 || files[0].Name != "Report.PDF" {
		t.Fatalf("PG name search must be case-insensitive: got total=%d files=%v", total, files)
	}

	// Path-prefix matching is case-sensitive (PG LIKE): marking children of
	// "Docs" must not touch "docs/..." — and escaped '_' must not leak into
	// the myXdir sibling either.
	if err := pgUnit(r, session.ID, "Docs"); err != nil {
		t.Fatalf("unit decide Docs: %v", err)
	}
	if tagged, _ := pgTagged(r, session.ID, "Docs/Report.PDF"); !tagged {
		t.Fatal("Docs/Report.PDF must be tagged")
	}
	if tagged, _ := pgTagged(r, session.ID, "docs/notes.txt"); tagged {
		t.Fatal("docs/notes.txt must NOT be tagged — PG LIKE is case-sensitive, prefix must match exactly")
	}
	if err := pgUnit(r, session.ID, "my_dir"); err != nil {
		t.Fatalf("unit decide my_dir: %v", err)
	}
	if tagged, _ := pgTagged(r, session.ID, "myXdir/b.txt"); tagged {
		t.Fatal("myXdir/b.txt must NOT be tagged — '_' leaked as a LIKE wildcard")
	}

	// Aggregate counts with boolean bind params (RefreshSessionCounts pattern).
	if err := r.RefreshSessionCounts(session.ID); err != nil {
		t.Fatalf("RefreshSessionCounts: %v", err)
	}
	reloaded, err := r.GetSession(session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if reloaded.TotalFiles != len(entries) || reloaded.TaggedFiles != 4 {
		t.Fatalf("session counts: got total=%d tagged=%d, want total=%d tagged=4 (two units, each with one member)",
			reloaded.TotalFiles, reloaded.TaggedFiles, len(entries))
	}
}

// The plan/memory/preview queries use dialect-specific pieces (ILIKE recall,
// quoted "key" column, strpos segment aggregation) — run them on PostgreSQL.
func TestPostgresBrainQueries(t *testing.T) {
	dsn := postgresTestDSN(t)
	if err := Init(config.DatabaseConfig{Driver: "postgres", DSN: dsn}); err != nil {
		t.Fatalf("init postgres: %v", err)
	}
	resetSchema(t)
	r := NewRepository(DB)

	seedPreviewFixture(t, r)
	checkPreviewAfter(t, r)
	checkPreviewBefore(t, r)

	if _, err := r.UpsertMemory(&MemoryItem{Scope: MemoryScopeFilesystem, ScopeID: 1, Kind: MemoryConvention, Key: "ext:.PSD", Content: "设计源文件"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := r.UpsertMemory(&MemoryItem{Scope: MemoryScopeFilesystem, ScopeID: 1, Kind: MemoryConvention, Key: "ext:.psd", Content: "设计源文件 v2"}); err != nil {
		t.Fatalf("upsert same key: %v", err)
	}
	got, err := r.SearchMemory(MemorySearch{Query: "PSD", Scopes: []MemoryScopeRef{{Scope: MemoryScopeFilesystem, ScopeID: 1}}})
	if err != nil || len(got) != 1 || got[0].Content != "设计源文件 v2" {
		t.Fatalf("case-insensitive recall on postgres: %+v %v", got, err)
	}

	if _, err := r.SavePlan(1, "# 方案", 0, ""); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if _, err := r.SavePlan(1, "stale", 0, ""); err != ErrPlanVersionConflict {
		t.Fatalf("stale plan: %v", err)
	}
}

func pgUnit(r *Repository, sessionID uint, p string) error {
	_, err := r.Decide(sessionID, Decision{Path: p, Role: RoleUnit, NewPath: "/归档/" + p, Override: true})
	return err
}

func pgTagged(r *Repository, sessionID uint, p string) (bool, error) {
	e, err := r.GetFileByPath(sessionID, p)
	if err != nil {
		return false, err
	}
	return e.Tagged, nil
}

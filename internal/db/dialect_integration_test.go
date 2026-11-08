package db

import (
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"FileEngine/internal/config"
)

// Server-backed dialect tests. MySQL runs when FILEENGINE_TEST_MYSQL_DSN
// points at a scratch database (it drops every table), e.g.
//
//	docker run --rm -d -e MYSQL_ROOT_PASSWORD=test -e MYSQL_DATABASE=fe -p 3306:3306 mysql:8.4
//	FILEENGINE_TEST_MYSQL_DSN="root:test@tcp(127.0.0.1:3306)/fe?parseTime=true" go test ./internal/db/ -run MySQL
//
// PostgreSQL uses FILEENGINE_TEST_PG_DSN (see postgres_integration_test.go).

func resetSchema(t *testing.T) {
	t.Helper()
	if err := DB.Migrator().DropTable(allModels...); err != nil {
		t.Fatalf("drop tables: %v", err)
	}
	if err := migrate(DB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

func TestMySQLDialectSemantics(t *testing.T) {
	dsn := os.Getenv("FILEENGINE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("FILEENGINE_TEST_MYSQL_DSN not set — skipping MySQL integration test")
	}
	cfg := config.DatabaseConfig{Driver: "mysql", DSN: dsn}
	if err := Init(cfg); err != nil {
		t.Fatalf("init mysql: %v", err)
	}
	resetSchema(t)
	if err := Init(cfg); err != nil {
		t.Fatalf("re-init mysql: %v", err)
	}
	checkPathSemantics(t, NewRepository(DB))
}

func TestPostgresPathSemantics(t *testing.T) {
	cfg := config.DatabaseConfig{Driver: "postgres", DSN: postgresTestDSN(t)}
	if err := Init(cfg); err != nil {
		t.Fatalf("init postgres: %v", err)
	}
	resetSchema(t)
	if err := Init(cfg); err != nil {
		t.Fatalf("re-init postgres: %v", err)
	}
	var coll string
	if err := DB.Raw(`SELECT collation_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'file_entries' AND column_name = 'original_path'`).Scan(&coll).Error; err != nil || coll != "C" {
		t.Fatalf("original_path collation: %q %v", coll, err)
	}
	checkPathSemantics(t, NewRepository(DB))
}

func TestSQLitePathSemantics(t *testing.T) {
	checkPathSemantics(t, setupBrainRepo(t))
}

func checkPathSemantics(t *testing.T, r *Repository) {
	t.Helper()
	fs := &Filesystem{Name: "fs", Protocol: "local"}
	if err := r.CreateFilesystem(fs); err != nil {
		t.Fatal(err)
	}
	s := &ScanSession{FilesystemID: fs.ID, Status: "scanned"}
	if err := r.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	paths := []string{"Docs", "Docs/Report.PDF", "docs", "docs/notes.txt", "Docs0/x", "Docs.bak/y", "Docs-1/z",
		"my_dir", "my_dir/a.txt", "myXdir/b.txt", "100%/c", "1000/d", "照片", "照片/e.jpg", "照片2/f.jpg", "trail /g"}
	for _, p := range paths {
		typ := "file"
		if !strings.Contains(p, ".") && !strings.Contains(p, "/") {
			typ = "directory"
		}
		seedEntry(t, r, s.ID, p, typ, 1)
	}
	for _, dir := range []string{"Docs", "docs", "my_dir", "100%", "照片", "trail "} {
		var got []string
		if err := entriesUnder(r.db, s.ID, dir).Pluck("original_path", &got).Error; err != nil {
			t.Fatal(err)
		}
		sort.Strings(got)
		var want []string
		for _, p := range paths {
			if strings.HasPrefix(p, dir+"/") {
				want = append(want, p)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("under %q: got %q, want %q", dir, got, want)
		}
	}

	if _, err := r.Decide(s.ID, Decision{Path: "docs/notes.txt", Role: RoleFile, NewPath: "/Photo/Café.txt"}); err != nil {
		t.Fatal(err)
	}
	res, err := r.Decide(s.ID, Decision{Path: "Docs/Report.PDF", Role: RoleFile, NewPath: "/photo/café.TXT"})
	if err != nil || res.NewPath != "/photo/café (2).TXT" {
		t.Fatalf("collision: %+v %v", res, err)
	}
	if _, err := r.Decide(s.ID, Decision{Path: "照片2/f.jpg", Role: RoleFile, NewPath: "/Photos2/f.jpg"}); err != nil {
		t.Fatal(err)
	}

	cat := &Category{FilesystemID: fs.ID, Name: "照片", Path: "/Photo"}
	if err := r.CreateCategory(cat); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateCategory(&Category{FilesystemID: fs.ID, Name: "照片2", Path: "/Photos2"}); err != nil {
		t.Fatal(err)
	}
	cat.Path = "/图片"
	if err := r.UpdateCategoryPath(cat, "/Photo"); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"docs/notes.txt": "/图片/Café.txt", "照片2/f.jpg": "/Photos2/f.jpg"} {
		e, err := r.GetFileByPath(s.ID, p)
		if err != nil || e.NewPath != want || e.NewPathKey != CollisionKey(want) {
			t.Errorf("%s after category move: %q %q", p, e.NewPath, e.NewPathKey)
		}
	}
	usage, err := r.GetCategoryUsageSummary(fs.ID, s.ID)
	if err != nil || len(usage) != 2 {
		t.Fatalf("usage: %+v %v", usage, err)
	}
	for _, u := range usage {
		if u.FileCount != 1 {
			t.Errorf("usage %s: %d", u.Path, u.FileCount)
		}
	}

	files, total, err := r.ListFiles(FileQuery{SessionID: s.ID, Search: "report"})
	if err != nil || total != 1 || files[0].Name != "Report.PDF" {
		t.Fatalf("name search: %d %v", total, err)
	}

	root := ""
	all, _, err := r.ListFiles(FileQuery{SessionID: s.ID, ParentPath: &root, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	var keyset []string
	var cursor uint
	for {
		page, more, err := r.ListFilesAfter(FileQuery{SessionID: s.ID, ParentPath: &root}, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range page {
			keyset = append(keyset, f.OriginalPath)
		}
		if !more {
			break
		}
		cursor = page[len(page)-1].ID
	}
	var offset []string
	for _, f := range all {
		offset = append(offset, f.OriginalPath)
	}
	if !slices.Equal(keyset, offset) || len(offset) != 4 {
		t.Fatalf("keyset %q vs offset %q", keyset, offset)
	}

	if sample, err := r.SampleFiles(FileQuery{SessionID: s.ID}, 5); err != nil || len(sample) != 5 {
		t.Fatalf("sample: %d %v", len(sample), err)
	}
	if err := r.RefreshSessionCounts(s.ID); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetSession(s.ID)
	if err != nil || got.TotalFiles != len(paths) || got.PlannedOps != 3 {
		t.Fatalf("counts: %+v %v", got, err)
	}
}

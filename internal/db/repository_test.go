package db

import (
	"path/filepath"
	"testing"

	"FileEngine/internal/config"
)

// GetCategoryUsageSummary must return the complete category system — unused
// categories included with zero counts — and must not let sibling path
// prefixes bleed into each other's counts.
func TestGetCategoryUsageSummaryComplete(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(DB)

	fs := &Filesystem{Name: "fs", Protocol: "local", BasePath: "/data"}
	if err := r.CreateFilesystem(fs); err != nil {
		t.Fatalf("create filesystem: %v", err)
	}
	session := &ScanSession{FilesystemID: fs.ID, Status: "tagging"}
	if err := r.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	for _, c := range []Category{
		{FilesystemID: fs.ID, Name: "照片", Path: "/photo"},
		{FilesystemID: fs.ID, Name: "照片备份", Path: "/photos2"},
		{FilesystemID: fs.ID, Name: "空分类", Path: "/empty"},
	} {
		cat := c
		if err := r.CreateCategory(&cat); err != nil {
			t.Fatalf("create category %s: %v", c.Name, err)
		}
	}

	files := []FileEntry{
		{ScanSessionID: session.ID, OriginalPath: "in/a.jpg", Name: "a.jpg", FileType: "file", NewPath: "/photo/a.jpg", Operation: "planned"},
		{ScanSessionID: session.ID, OriginalPath: "in/b.jpg", Name: "b.jpg", FileType: "file", NewPath: "/photo/sub/b.jpg", Operation: "planned"},
		{ScanSessionID: session.ID, OriginalPath: "in/c.jpg", Name: "c.jpg", FileType: "file", NewPath: "/photos2/c.jpg", Operation: "planned"},
		{ScanSessionID: session.ID, OriginalPath: "in/d.jpg", Name: "d.jpg", FileType: "file"}, // unplanned
	}
	for _, f := range files {
		fe := f
		if err := r.CreateFile(&fe); err != nil {
			t.Fatalf("create file %s: %v", f.OriginalPath, err)
		}
	}

	usages, err := r.GetCategoryUsageSummary(fs.ID, session.ID)
	if err != nil {
		t.Fatalf("GetCategoryUsageSummary: %v", err)
	}
	if len(usages) != 3 {
		t.Fatalf("usages: got %d entries, want 3 (all categories incl. unused): %+v", len(usages), usages)
	}

	byPath := map[string]int64{}
	for _, u := range usages {
		byPath[u.Path] = u.FileCount
	}
	if byPath["/photo"] != 2 {
		t.Fatalf("/photo count: got %d, want 2 (prefix collision with /photos2?)", byPath["/photo"])
	}
	if byPath["/photos2"] != 1 {
		t.Fatalf("/photos2 count: got %d, want 1", byPath["/photos2"])
	}
	if c, ok := byPath["/empty"]; !ok || c != 0 {
		t.Fatalf("/empty: got count=%d present=%v, want 0 present", c, ok)
	}

	// GetSessionStats applies the same exact-prefix matching (used-only view).
	stats, err := r.GetSessionStats(session.ID, fs.ID)
	if err != nil {
		t.Fatalf("GetSessionStats: %v", err)
	}
	statByPath := map[string]int64{}
	for _, s := range stats {
		statByPath[s.Path] = s.FileCount
	}
	if statByPath["/photo"] != 2 || statByPath["/photos2"] != 1 {
		t.Fatalf("stats counts: got %v, want /photo=2 /photos2=1", statByPath)
	}
	if _, ok := statByPath["/empty"]; ok {
		t.Fatal("stats must not include unused categories")
	}
}

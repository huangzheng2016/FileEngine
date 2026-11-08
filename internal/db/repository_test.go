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

// allow_script defaults ON: a session created without touching the field must
// read back AllowScript=true (the GORM default tag omits the zero value from
// the INSERT, so the DB default applies).
func TestScanSessionAllowScriptDefaultsOn(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(DB)

	fs := &Filesystem{Name: "fs", Protocol: "local", BasePath: "/data"}
	if err := r.CreateFilesystem(fs); err != nil {
		t.Fatalf("create filesystem: %v", err)
	}
	session := &ScanSession{FilesystemID: fs.ID, Status: "scanning"}
	if err := r.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	reloaded, err := r.GetSession(session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if !reloaded.AllowScript {
		t.Fatal("AllowScript must default to true for a session created without setting it")
	}

	// An explicit false persists (the session switch can be turned off).
	reloaded.AllowScript = false
	if err := r.UpdateSession(reloaded); err != nil {
		t.Fatalf("update session: %v", err)
	}
	again, err := r.GetSession(session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if again.AllowScript {
		t.Fatal("explicit AllowScript=false must persist")
	}
}

// SampleFiles returns a random subset bounded by n, honoring the query
// filters, and never invents rows.
func TestSampleFiles(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(DB)

	for _, f := range []FileEntry{
		{ScanSessionID: 1, OriginalPath: "in/a.jpg", ParentPath: "in", Name: "a.jpg", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "in/b.jpg", ParentPath: "in", Name: "b.jpg", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "in/c.jpg", ParentPath: "in", Name: "c.jpg", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "in/d.jpg", ParentPath: "in", Name: "d.jpg", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "in", ParentPath: "", Name: "in", FileType: "directory"},
		{ScanSessionID: 2, OriginalPath: "other/x.jpg", ParentPath: "other", Name: "x.jpg", FileType: "file"},
	} {
		fe := f
		if err := r.CreateFile(&fe); err != nil {
			t.Fatalf("create file %s: %v", f.OriginalPath, err)
		}
	}

	parent := "in"
	// n above the match count returns every match (all session-1 "in" files).
	all, err := r.SampleFiles(FileQuery{SessionID: 1, ParentPath: &parent, FileType: "file"}, 50)
	if err != nil {
		t.Fatalf("SampleFiles: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("SampleFiles n>matches: got %d, want 4", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].OriginalPath > all[i].OriginalPath {
			t.Fatalf("SampleFiles output must be path-sorted: %v", all)
		}
	}

	// n caps the subset; every winner matches the filters and session.
	some, err := r.SampleFiles(FileQuery{SessionID: 1, ParentPath: &parent}, 2)
	if err != nil {
		t.Fatalf("SampleFiles: %v", err)
	}
	if len(some) != 2 {
		t.Fatalf("SampleFiles n=2: got %d", len(some))
	}
	for _, f := range some {
		if f.ScanSessionID != 1 || f.ParentPath != "in" {
			t.Fatalf("sampled row violates filters: %+v", f)
		}
	}

	// No matches → empty, not an error.
	none, err := r.SampleFiles(FileQuery{SessionID: 99}, 5)
	if err != nil || len(none) != 0 {
		t.Fatalf("SampleFiles no matches: got %v, %v", none, err)
	}
	if got, err := r.SampleFiles(FileQuery{SessionID: 1}, 0); err != nil || got != nil {
		t.Fatalf("SampleFiles n=0: got %v, %v", got, err)
	}
}

// CountUntaggedByType splits the untagged remainder by entry type — the
// reconciliation line depends on directories/symlinks being reported
// separately from files (only files signal tagging misses).
func TestCountUntaggedByType(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(DB)

	entries := []FileEntry{
		{ScanSessionID: 1, OriginalPath: "in/dir", Name: "dir", FileType: "directory"},
		{ScanSessionID: 1, OriginalPath: "in/dir2", Name: "dir2", FileType: "directory", Tagged: true},
		{ScanSessionID: 1, OriginalPath: "in/a.txt", Name: "a.txt", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "in/b.txt", Name: "b.txt", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "in/c.txt", Name: "c.txt", FileType: "file", Tagged: true},
		{ScanSessionID: 1, OriginalPath: "in/link", Name: "link", FileType: "symlink"},
		// Another session's untagged entries must not leak in.
		{ScanSessionID: 2, OriginalPath: "in/x.txt", Name: "x.txt", FileType: "file"},
	}
	for _, f := range entries {
		fe := f
		if err := r.CreateFile(&fe); err != nil {
			t.Fatalf("create file %s: %v", f.OriginalPath, err)
		}
	}

	b, err := r.CountUntaggedByType(1)
	if err != nil {
		t.Fatalf("CountUntaggedByType: %v", err)
	}
	if b.Directories != 1 || b.Files != 2 || b.Symlinks != 1 {
		t.Fatalf("breakdown: got %+v, want dirs=1 files=2 symlinks=1", b)
	}
	if b.Total() != 4 {
		t.Fatalf("total: got %d, want 4", b.Total())
	}

	empty, err := r.CountUntaggedByType(99)
	if err != nil || empty.Total() != 0 {
		t.Fatalf("unknown session: got %+v, %v", empty, err)
	}
}

// TaggedStatusByIDs backs the loose-file batch re-check: it must report the
// stored tagged flag per ID (unknown IDs simply absent).
func TestTaggedStatusByIDs(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(DB)

	tagged := &FileEntry{ScanSessionID: 1, OriginalPath: "in/a.txt", Name: "a.txt", FileType: "file", Tagged: true}
	untagged := &FileEntry{ScanSessionID: 1, OriginalPath: "in/b.txt", Name: "b.txt", FileType: "file"}
	for _, f := range []*FileEntry{tagged, untagged} {
		if err := r.CreateFile(f); err != nil {
			t.Fatalf("create file: %v", err)
		}
	}

	got, err := r.TaggedStatusByIDs([]uint{tagged.ID, untagged.ID})
	if err != nil {
		t.Fatalf("TaggedStatusByIDs: %v", err)
	}
	if !got[tagged.ID] {
		t.Fatalf("tagged file reported untagged: %v", got)
	}
	if got[untagged.ID] {
		t.Fatalf("untagged file reported tagged: %v", got)
	}

	if got, err := r.TaggedStatusByIDs(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty ids: got %v, %v", got, err)
	}
}

// LIKE wildcards in directory names must match literally: marking children of
// "in/my_dir" must not cascade into the sibling "in/myXdir" (an unescaped '_'
// is a single-character wildcard in every dialect).
func TestUnitMembersLiteralUnderscore(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(DB)

	entries := []FileEntry{
		{ScanSessionID: 1, OriginalPath: "in/my_dir", Name: "my_dir", FileType: "directory"},
		{ScanSessionID: 1, OriginalPath: "in/my_dir/a.txt", Name: "a.txt", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "in/myXdir", Name: "myXdir", FileType: "directory"},
		{ScanSessionID: 1, OriginalPath: "in/myXdir/b.txt", Name: "b.txt", FileType: "file"},
	}
	for _, f := range entries {
		fe := f
		if err := r.CreateFile(&fe); err != nil {
			t.Fatalf("create file %s: %v", f.OriginalPath, err)
		}
	}

	if err := pgUnit(r, 1, "in/my_dir"); err != nil {
		t.Fatalf("MarkChildrenTagged: %v", err)
	}

	tagged, err := pgTagged(r, 1, "in/my_dir/a.txt")
	if err != nil {
		t.Fatalf("IsFileTagged a.txt: %v", err)
	}
	if !tagged {
		t.Fatal("child of my_dir must be tagged")
	}
	sibling, err := pgTagged(r, 1, "in/myXdir/b.txt")
	if err != nil {
		t.Fatalf("IsFileTagged b.txt: %v", err)
	}
	if sibling {
		t.Fatal("sibling myXdir must NOT be tagged — '_' in my_dir leaked as a LIKE wildcard")
	}
}

// Name search stays case-insensitive on SQLite (and must treat a literal '%'
// in the query as text, not as a wildcard).
func TestListFilesSearchCaseAndEscapes(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(DB)

	for _, f := range []FileEntry{
		{ScanSessionID: 1, OriginalPath: "in/Photo.JPG", Name: "Photo.JPG", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "in/100%.txt", Name: "100%.txt", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "in/1000x.txt", Name: "1000x.txt", FileType: "file"},
	} {
		fe := f
		if err := r.CreateFile(&fe); err != nil {
			t.Fatalf("create file %s: %v", f.OriginalPath, err)
		}
	}

	files, total, err := r.ListFiles(FileQuery{SessionID: 1, Search: "photo"})
	if err != nil {
		t.Fatalf("ListFiles search photo: %v", err)
	}
	if total != 1 || len(files) != 1 || files[0].Name != "Photo.JPG" {
		t.Fatalf("case-insensitive search: got total=%d files=%v", total, files)
	}

	files, total, err = r.ListFiles(FileQuery{SessionID: 1, Search: "100%"})
	if err != nil {
		t.Fatalf("ListFiles search 100%%: %v", err)
	}
	if total != 1 || len(files) != 1 || files[0].Name != "100%.txt" {
		t.Fatalf("literal %% search must match only 100%%.txt: got total=%d files=%v", total, files)
	}
}

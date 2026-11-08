package db

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Subtree ranges must match exactly the "dir/" byte prefix, whatever bytes
// the directory name holds (LIKE metacharacters, the escape char, unicode,
// the bytes around '/').
func TestSubtreeCondMatchesExactPrefix(t *testing.T) {
	r := setupBrainRepo(t)
	paths := []string{
		"a", "a/x", "a/x/y", "a0", "a0/z", "a.b", "a.b/c", "A/x", "a!/x", "a-/x",
		"100%", "100%/f", "100x/f", "1000/f",
		"my_dir", "my_dir/f", "myXdir/f", "my!_dir/f",
		"b!c", "b!c/f", "b!!c/f",
		"照片", "照片/2024/a.jpg", "照片2/b.jpg", "照/片", "Café/x", "Café/x",
	}
	for _, p := range paths {
		seedEntry(t, r, 1, p, "file", 1)
	}
	for _, dir := range []string{"a", "a/x", "100%", "my_dir", "b!c", "照片", "Café", ""} {
		var got []string
		if err := entriesUnder(r.db, 1, dir).Order("original_path").Pluck("original_path", &got).Error; err != nil {
			t.Fatal(err)
		}
		var want []string
		for _, p := range paths {
			if strings.HasPrefix(p, dir+"/") {
				want = append(want, p)
			}
		}
		sort.Strings(want)
		if !slices.Equal(got, want) {
			t.Errorf("under %q: got %q, want %q", dir, got, want)
		}
	}
}

// quietRepo drops GORM's slow-query log (bulk fixtures trip it).
func quietRepo(r *Repository) *Repository {
	return NewRepository(r.db.Session(&gorm.Session{Logger: logger.Discard}))
}

// explain returns SQLite's plan for the statement a GORM builder produces.
func explain(t *testing.T, db *gorm.DB, build func(tx *gorm.DB) *gorm.DB) string {
	t.Helper()
	sql := db.ToSQL(build)
	rows, err := db.Raw("EXPLAIN QUERY PLAN " + sql).Rows()
	if err != nil {
		t.Fatalf("explain %s: %v", sql, err)
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		sb.WriteString(detail + "\n")
	}
	return sb.String()
}

// With ~50k rows every subtree/target/keyset/collision query runs on an
// index: no full scan of file_entries and no temp sort for listings.
func TestPathQueriesUseIndexes(t *testing.T) {
	r := quietRepo(setupBrainRepo(t))
	rows := make([]FileEntry, 0, 50000)
	for i := 0; i < 50000; i++ {
		dir := fmt.Sprintf("d%03d", i%500)
		p := fmt.Sprintf("%s/f%05d.txt", dir, i)
		np := fmt.Sprintf("/目标/%s/f%05d.txt", dir, i)
		rows = append(rows, FileEntry{ScanSessionID: 1 + uint(i%2), OriginalPath: p, ParentPath: dir, Name: p[len(dir)+1:],
			FileType: "file", NewPath: np, NewPathKey: CollisionKey(np), Operation: "planned", Role: RoleFile})
	}
	if err := r.CreateFiles(rows); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Exec("ANALYZE").Error; err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, index string
		build       func(tx *gorm.DB) *gorm.DB
	}{
		{"subtree update", "idx_uniq_session_path", func(tx *gorm.DB) *gorm.DB {
			return entriesUnder(tx, 1, "d007").Updates(map[string]any{"tagged": true})
		}},
		{"target count", "idx_session_newpath", func(tx *gorm.DB) *gorm.DB {
			cond, args := targetCond(tx, "/目标/d007")
			var n int64
			return tx.Model(&FileEntry{}).Where("scan_session_id = ?", 1).Where(cond, args...).Count(&n)
		}},
		{"collision key", "idx_session_newkey", func(tx *gorm.DB) *gorm.DB {
			var ids []uint
			return tx.Model(&FileEntry{}).Where("scan_session_id = ? AND new_path_key = ? AND original_path <> ?", 1, "目标/d007/f00007.txt", "x").Limit(1).Pluck("id", &ids)
		}},
		{"keyset page", "idx_session_parent", func(tx *gorm.DB) *gorm.DB {
			parent := "d007"
			var fs []FileEntry
			return applyFileFilters(tx.Model(&FileEntry{}), FileQuery{SessionID: 1, ParentPath: &parent}).
				Where("(file_type, name, id) > (?, ?, ?)", "file", "f00007.txt", 7).Order(fileOrder).Limit(101).Find(&fs)
		}},
		{"role filter", "idx_session_role", func(tx *gorm.DB) *gorm.DB {
			var n int64
			return tx.Model(&FileEntry{}).Where("scan_session_id = ? AND role = ?", 1, RoleUnit).Count(&n)
		}},
	}
	for _, tc := range cases {
		plan := explain(t, r.db, tc.build)
		if !strings.Contains(plan, "USING INDEX "+tc.index) && !strings.Contains(plan, "USING COVERING INDEX "+tc.index) {
			t.Errorf("%s: want index %s, plan:\n%s", tc.name, tc.index, plan)
		}
		if strings.Contains(plan, "TEMP B-TREE") || strings.Contains(plan, "SCAN file_entries\n") {
			t.Errorf("%s: scan or temp sort, plan:\n%s", tc.name, plan)
		}
	}
	// The real subtree write touches exactly the subtree.
	res := entriesUnder(r.db, 1, "d008").Updates(map[string]any{"tagged": true})
	if res.Error != nil || res.RowsAffected != 100 {
		t.Fatalf("subtree update: %d %v", res.RowsAffected, res.Error)
	}
}

func BenchmarkSubtreeUpdate(b *testing.B) {
	r := quietRepo(setupBrainRepo(b))
	rows := make([]FileEntry, 0, 50000)
	for i := 0; i < 50000; i++ {
		dir := fmt.Sprintf("d%03d", i%500)
		rows = append(rows, FileEntry{ScanSessionID: 1, OriginalPath: fmt.Sprintf("%s/f%05d", dir, i), ParentPath: dir, FileType: "file"})
	}
	if err := r.CreateFiles(rows); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := entriesUnder(r.db, 1, fmt.Sprintf("d%03d", i%500)).Update("tagged", i%2 == 0).Error; err != nil {
			b.Fatal(err)
		}
	}
}

func TestRefreshSessionCountsWritesOnlyOnChange(t *testing.T) {
	r := setupBrainRepo(t)
	s := &ScanSession{Status: "scanned"}
	if err := r.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	seedEntry(t, r, s.ID, "a.txt", "file", 5)
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	stamp := func() {
		if err := r.db.Model(&ScanSession{}).Where("id = ?", s.ID).UpdateColumn("updated_at", past).Error; err != nil {
			t.Fatal(err)
		}
	}
	reload := func() *ScanSession {
		got, err := r.GetSession(s.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if err := r.RefreshSessionCounts(s.ID); err != nil {
		t.Fatal(err)
	}
	if got := reload(); got.TotalFiles != 1 || got.TotalSize != 5 {
		t.Fatalf("counts: %+v", got)
	}
	stamp()
	if err := r.RefreshSessionCounts(s.ID); err != nil {
		t.Fatal(err)
	}
	if got := reload(); !got.UpdatedAt.Equal(past) {
		t.Fatalf("unchanged counters must not write: updated_at %v", got.UpdatedAt)
	}
}

// Decisions refresh the stored counters asynchronously and coalesced.
func TestDecisionsRefreshCountsCoalesced(t *testing.T) {
	old := countsDelay
	countsDelay = 20 * time.Millisecond
	defer func() { countsDelay = old }()
	r := setupBrainRepo(t)
	s := &ScanSession{Status: "scanned"}
	if err := r.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		seedEntry(t, r, s.ID, fmt.Sprintf("f%d.txt", i), "file", 1)
	}
	for i := 0; i < 5; i++ {
		if _, err := r.Decide(s.ID, Decision{Path: fmt.Sprintf("f%d.txt", i), Role: RoleFile, NewPath: "/t/" + fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, _ := r.GetSession(s.ID)
		if got.PlannedOps == 5 && got.TaggedFiles == 5 && got.TotalFiles == 5 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("counters never refreshed: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUpdateCategoryPathSwapsPrefixOnly(t *testing.T) {
	r := setupBrainRepo(t)
	fsA, fsB := &Filesystem{Name: "a"}, &Filesystem{Name: "b"}
	for _, f := range []*Filesystem{fsA, fsB} {
		if err := r.CreateFilesystem(f); err != nil {
			t.Fatal(err)
		}
	}
	sA, sB := &ScanSession{FilesystemID: fsA.ID}, &ScanSession{FilesystemID: fsB.ID}
	for _, s := range []*ScanSession{sA, sB} {
		if err := r.CreateSession(s); err != nil {
			t.Fatal(err)
		}
	}
	targets := map[string]string{"1": "/Photo", "2": "/Photo/2024/a.jpg", "3": "/Photos2/b.jpg", "4": "/x/Photo/c.jpg", "5": "/Photo/Photo/d.jpg"}
	for name, target := range targets {
		seedEntry(t, r, sA.ID, name, "file", 1)
		if _, err := r.Decide(sA.ID, Decision{Path: name, Role: RoleFile, NewPath: target}); err != nil {
			t.Fatal(err)
		}
	}
	seedEntry(t, r, sB.ID, "other", "file", 1)
	if _, err := r.Decide(sB.ID, Decision{Path: "other", Role: RoleFile, NewPath: "/Photo/z.jpg"}); err != nil {
		t.Fatal(err)
	}
	cat := &Category{FilesystemID: fsA.ID, Name: "照片", Path: "/Photo"}
	if err := r.CreateCategory(cat); err != nil {
		t.Fatal(err)
	}
	cat.Path = "/图片"
	if err := r.UpdateCategoryPath(cat, "/Photo"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"1": "/图片", "2": "/图片/2024/a.jpg", "3": "/Photos2/b.jpg", "4": "/x/Photo/c.jpg", "5": "/图片/Photo/d.jpg"}
	for name, w := range want {
		e, err := r.GetFileByPath(sA.ID, name)
		if err != nil || e.NewPath != w || e.NewPathKey != CollisionKey(w) {
			t.Errorf("%s: got %q key %q, want %q", name, e.NewPath, e.NewPathKey, w)
		}
	}
	if e, _ := r.GetFileByPath(sB.ID, "other"); e.NewPath != "/Photo/z.jpg" {
		t.Errorf("other filesystem touched: %q", e.NewPath)
	}
	// The rewritten key still detects collisions (case-insensitive).
	seedEntry(t, r, sA.ID, "6", "file", 1)
	res, err := r.Decide(sA.ID, Decision{Path: "6", Role: RoleFile, NewPath: "/图片/2024/A.JPG"})
	if err != nil || res.NewPath != "/图片/2024/A (2).JPG" {
		t.Fatalf("collision after rename: %+v %v", res, err)
	}
}

func TestCollisionKeyColumn(t *testing.T) {
	r := setupBrainRepo(t)
	for _, p := range []string{"a", "b", "c"} {
		seedEntry(t, r, 1, p, "file", 1)
	}
	if _, err := r.Decide(1, Decision{Path: "a", Role: RoleFile, NewPath: "/T/Café.txt"}); err != nil {
		t.Fatal(err)
	}
	res, err := r.Decide(1, Decision{Path: "b", Role: RoleFile, NewPath: "/t/café.TXT"})
	if err != nil || res.NewPath != "/t/café (2).TXT" {
		t.Fatalf("NFD/case variant must collide: %+v %v", res, err)
	}
	// Re-deciding the same entry to its own target is not a collision.
	if res, err := r.Decide(1, Decision{Path: "a", Role: RoleFile, NewPath: "/T/Café.txt"}); err != nil || res.Renamed {
		t.Fatalf("self target: %+v %v", res, err)
	}
	if _, err := r.Decide(1, Decision{Path: "a", Role: RoleUndecided}); err != nil {
		t.Fatal(err)
	}
	if e := entry(t, r, "a"); e.NewPathKey != "" {
		t.Fatalf("undecide clears the key: %q", e.NewPathKey)
	}
	if res, err := r.Decide(1, Decision{Path: "c", Role: RoleFile, NewPath: "/T/Café.txt"}); err != nil || res.Renamed {
		t.Fatalf("freed target is reusable: %+v %v", res, err)
	}
}

func TestListFilesAfterKeyset(t *testing.T) {
	r := setupBrainRepo(t)
	var rows []FileEntry
	for i := 0; i < 523; i++ {
		typ := "file"
		if i%7 == 0 {
			typ = "directory"
		}
		name := fmt.Sprintf("n%03d", i%200) // duplicate names exercise the id tie-break
		rows = append(rows, FileEntry{ScanSessionID: 1, OriginalPath: fmt.Sprintf("p/%d", i), ParentPath: "p", Name: name, FileType: typ})
	}
	if err := r.CreateFiles(rows); err != nil {
		t.Fatal(err)
	}
	parent := "p"
	q := FileQuery{SessionID: 1, ParentPath: &parent}
	all, total, err := r.ListFiles(FileQuery{SessionID: 1, ParentPath: &parent, PageSize: 1000})
	if err != nil || total != 523 {
		t.Fatalf("list: %d %v", total, err)
	}
	var got []uint
	var cursor uint
	for pages := 0; ; pages++ {
		page, more, err := r.ListFilesAfter(q, cursor, 50)
		if err != nil || pages > 20 {
			t.Fatalf("page: %v", err)
		}
		for _, f := range page {
			got = append(got, f.ID)
		}
		if !more {
			break
		}
		cursor = page[len(page)-1].ID
	}
	want := make([]uint, len(all))
	for i, f := range all {
		want[i] = f.ID
	}
	if !slices.Equal(got, want) {
		t.Fatalf("keyset order differs from offset order (%d vs %d rows)", len(got), len(want))
	}
	if _, _, err := r.ListFilesAfter(q, 999999, 50); err != ErrBadCursor {
		t.Fatalf("unknown cursor: %v", err)
	}
}

func TestSampleFilesDistinctAndFiltered(t *testing.T) {
	r := setupBrainRepo(t)
	var rows []FileEntry
	for i := 0; i < 1000; i++ {
		typ := "file"
		if i%3 == 0 {
			typ = "directory"
		}
		rows = append(rows, FileEntry{ScanSessionID: 1, OriginalPath: fmt.Sprintf("f%04d", i), Name: fmt.Sprintf("f%04d", i), FileType: typ})
	}
	if err := r.CreateFiles(rows); err != nil {
		t.Fatal(err)
	}
	got, err := r.SampleFiles(FileQuery{SessionID: 1, FileType: "file"}, 50)
	if err != nil || len(got) != 50 {
		t.Fatalf("sample: %d %v", len(got), err)
	}
	seen := map[uint]bool{}
	for i, f := range got {
		if f.FileType != "file" || seen[f.ID] || (i > 0 && got[i-1].OriginalPath > f.OriginalPath) {
			t.Fatalf("sample must be distinct, filtered and path-sorted: %+v", f)
		}
		seen[f.ID] = true
	}
	small, err := r.SampleFiles(FileQuery{SessionID: 1, Search: "f000"}, 50)
	if err != nil || len(small) != 10 {
		t.Fatalf("fewer matches than n returns them all: %d %v", len(small), err)
	}
}

func TestInsertBatchFitsSQLiteVariableLimit(t *testing.T) {
	r := setupBrainRepo(t)
	stmt := &gorm.Statement{DB: r.db}
	if err := stmt.Parse(&FileEntry{}); err != nil {
		t.Fatal(err)
	}
	if vars := r.InsertBatchRows() * len(stmt.Schema.DBNames); vars > 32766 {
		t.Fatalf("multi-row insert binds %d variables", vars)
	}
	rows := make([]FileEntry, 3000)
	for i := range rows {
		rows[i] = FileEntry{ScanSessionID: 1, OriginalPath: fmt.Sprintf("x/%d", i), FileType: "file"}
	}
	if err := r.CreateFiles(rows); err != nil {
		t.Fatal(err)
	}
}

// Every index must fit InnoDB's 3072-byte key at 4 bytes per utf8mb4 char
// once mysqlIndexPrefixes has applied its prefixes, and no indexed string
// column may lack a size (MySQL cannot index unbounded TEXT).
func TestMySQLIndexWidths(t *testing.T) {
	cache := &sync.Map{}
	for _, m := range allModels {
		s, err := schema.Parse(m, cache, schema.NamingStrategy{})
		if err != nil {
			t.Fatal(err)
		}
		for _, idx := range s.ParseIndexes() {
			for _, o := range idx.Fields {
				if o.Field.DataType == schema.String && o.Field.Size == 0 {
					t.Errorf("%s.%s: indexed string column %s has no size", s.Table, idx.Name, o.Field.DBName)
				}
			}
			if n := mysqlIndexBytes(idx.Fields); n > mysqlMaxKeyBytes {
				t.Errorf("%s.%s: %d bytes", s.Table, idx.Name, n)
			}
		}
	}
	// The unique path index keeps its full session column plus a long prefix.
	s, _ := schema.Parse(&FileEntry{}, cache, schema.NamingStrategy{})
	for _, idx := range s.ParseIndexes() {
		if idx.Name == "idx_uniq_session_path" {
			opts := mysqlIndexPrefixes(idx.Fields)
			if opts[0].Length != 0 || opts[1].Length != 766 {
				t.Fatalf("prefixes: %d %d", opts[0].Length, opts[1].Length)
			}
		}
	}
}

func TestCategoryStatsGrouped(t *testing.T) {
	r := setupBrainRepo(t)
	fs := &Filesystem{Name: "fs"}
	if err := r.CreateFilesystem(fs); err != nil {
		t.Fatal(err)
	}
	s := &ScanSession{FilesystemID: fs.ID}
	if err := r.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	// More categories than one UNION ALL chunk.
	for i := 0; i < 250; i++ {
		if err := r.CreateCategory(&Category{FilesystemID: fs.ID, Name: fmt.Sprintf("c%03d", i), Path: fmt.Sprintf("/c%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i, target := range []string{"/c007/a", "/c007/b", "/c0070/x", "/c249", "/c249/z"} {
		p := fmt.Sprintf("f%d", i)
		seedEntry(t, r, s.ID, p, "file", int64(10*(i+1)))
		if _, err := r.Decide(s.ID, Decision{Path: p, Role: RoleFile, NewPath: target}); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := r.GetCategoryUsageSummary(fs.ID, s.ID)
	if err != nil || len(usage) != 250 {
		t.Fatalf("usage: %d %v", len(usage), err)
	}
	counts := map[string]int64{}
	for _, u := range usage {
		counts[u.Path] = u.FileCount
	}
	if counts["/c007"] != 2 || counts["/c249"] != 2 || counts["/c070"] != 0 || counts["/c000"] != 0 {
		t.Fatalf("counts: c007=%d c249=%d", counts["/c007"], counts["/c249"])
	}
	stats, err := r.GetSessionStats(s.ID, fs.ID)
	if err != nil || len(stats) != 2 || stats[0].Path != "/c007" || stats[0].TotalSize != 30 || stats[1].TotalSize != 90 {
		t.Fatalf("stats: %+v %v", stats, err)
	}
}

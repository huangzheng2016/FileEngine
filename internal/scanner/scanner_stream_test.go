package scanner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

func scanSession(t *testing.T, repo *db.Repository, fs remotefs.RemoteFS, root string, mut func(*db.ScanSession)) (*Scanner, *db.ScanSession) {
	t.Helper()
	session := &db.ScanSession{RootPath: root, Status: "scanning"}
	if mut != nil {
		mut(session)
	}
	if err := repo.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sc := New(fs, repo, "", nil)
	if err := sc.Scan(context.Background(), session); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return sc, session
}

func entry(t *testing.T, repo *db.Repository, sessionID uint, p string) *db.FileEntry {
	t.Helper()
	f, err := repo.GetFileByPath(sessionID, p)
	if err != nil {
		t.Fatalf("GetFileByPath(%s): %v", p, err)
	}
	return f
}

func TestScanStream_CommitsInBatchesWithCounts(t *testing.T) {
	prev := commitRows
	commitRows = 50
	defer func() { commitRows = prev }()

	children := make([]*memNode, 0, 41)
	for i := 0; i < 40; i++ {
		children = append(children, dir(fmt.Sprintf("d%02d", i), file("a"), file("b"), dir("sub", file("c"))))
	}
	children = append(children, file("top.txt"))
	fs := &fakeFS{root: &memNode{name: "root", isDir: true, children: children}}

	repo := setupFileTestDB(t)
	sc, session := scanSession(t, repo, fs, "root", nil)
	want := 40*5 + 1
	var n int64
	repo.DB().Model(&db.FileEntry{}).Where("scan_session_id = ?", session.ID).Count(&n)
	if int(n) != want || session.TotalFiles != want || session.ScannedCount != want {
		t.Errorf("rows=%d total=%d scanned=%d, want %d", n, session.TotalFiles, session.ScannedCount, want)
	}
	if sc.commits < 3 {
		t.Errorf("commits = %d, want several (streaming)", sc.commits)
	}
	d := entry(t, repo, session.ID, "root/d07")
	if d.ChildCount != 3 || d.SubdirCount != 1 {
		t.Errorf("d07 child=%d subdir=%d, want 3/1", d.ChildCount, d.SubdirCount)
	}
	if sub := entry(t, repo, session.ID, "root/d07/sub"); sub.ChildCount != 1 || sub.SubdirCount != 0 {
		t.Errorf("sub child=%d subdir=%d, want 1/0", sub.ChildCount, sub.SubdirCount)
	}
}

func TestScanRescan_KeepsDecisionsOfUnchangedEntries(t *testing.T) {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	keep := &memNode{name: "keep.txt", size: 10, mtime: t0}
	changed := &memNode{name: "changed.txt", size: 10, mtime: t0}
	gone := &memNode{name: "gone.txt", size: 10, mtime: t0}
	proj := dir("proj", &memNode{name: "main.go", size: 5, mtime: t0})
	root := dir("root", keep, changed, gone, proj)
	fs := &fakeFS{root: root}

	var hooked []uint
	ApplyRulesHook = func(id uint) { hooked = append(hooked, id) }
	defer func() { ApplyRulesHook = nil }()

	repo := setupFileTestDB(t)
	_, session := scanSession(t, repo, fs, "root", nil)
	decide := func(p, role, target string) {
		repo.DB().Model(&db.FileEntry{}).Where("scan_session_id = ? AND original_path = ?", session.ID, p).
			Updates(map[string]any{"role": role, "new_path": target, "reason": "agent", "tagged": true})
	}
	decide("root/keep.txt", db.RoleFile, "docs/keep.txt")
	decide("root/changed.txt", db.RoleFile, "docs/changed.txt")
	decide("root/proj", db.RoleUnit, "code/proj")
	decide("root/proj/main.go", db.RoleMember, "")
	keepID := entry(t, repo, session.ID, "root/keep.txt").ID

	changed.size = 99
	root.children = []*memNode{keep, changed, proj, file("new.txt")}
	proj.children = append(proj.children, file("added.go"))

	sc := New(fs, repo, "", nil)
	if err := sc.Scan(context.Background(), session); err != nil {
		t.Fatalf("rescan: %v", err)
	}

	k := entry(t, repo, session.ID, "root/keep.txt")
	if k.ID != keepID || k.Role != db.RoleFile || k.NewPath != "docs/keep.txt" || k.Reason != "agent" || !k.Tagged {
		t.Errorf("unchanged entry lost its decision: %+v", k)
	}
	c := entry(t, repo, session.ID, "root/changed.txt")
	if c.Role != db.RoleUndecided || c.NewPath != "" || c.Tagged || c.Size != 99 {
		t.Errorf("changed entry not reset: role=%q new=%q tagged=%v size=%d", c.Role, c.NewPath, c.Tagged, c.Size)
	}
	if _, err := repo.GetFileByPath(session.ID, "root/gone.txt"); err == nil {
		t.Error("vanished entry still present")
	}
	if n := entry(t, repo, session.ID, "root/new.txt"); n.Role != db.RoleUndecided {
		t.Errorf("new entry role = %q", n.Role)
	}
	if a := entry(t, repo, session.ID, "root/proj/added.go"); a.Role != db.RoleMember || !a.Tagged {
		t.Errorf("new entry inside a unit: role=%q tagged=%v, want member", a.Role, a.Tagged)
	}
	if p := entry(t, repo, session.ID, "root/proj"); p.Role != db.RoleUnit || p.ChildCount != 2 {
		t.Errorf("unit dir: role=%q child=%d", p.Role, p.ChildCount)
	}
	if session.TotalFiles != 6 {
		t.Errorf("TotalFiles = %d, want 6", session.TotalFiles)
	}
	if len(hooked) != 2 || hooked[1] != session.ID {
		t.Errorf("ApplyRulesHook calls = %v, want one per scan", hooked)
	}
}

func TestParseImageMeta_ExifFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/exif.jpg")
	if err != nil {
		t.Fatal(err)
	}
	info := parseImageMeta(b)
	want := time.Date(2021, 7, 4, 9, 30, 0, 0, time.UTC)
	if info.takenAt == nil || !info.takenAt.Equal(want) || info.source != DateSourceExif {
		t.Fatalf("taken=%v source=%q, want %v exif", info.takenAt, info.source, want)
	}
	if info.camera != "TestCam X1" || !info.hasGPS {
		t.Errorf("camera=%q gps=%v", info.camera, info.hasGPS)
	}
	// HEIC-style: the same EXIF blob embedded somewhere in a container.
	heic := append([]byte("\x00\x00\x00\x18ftypheic....meta"), append([]byte("Exif\x00\x00"), buildTIFF("2020:02:02 02:02:02", "Phone")...)...)
	if info := parseImageMeta(heic); info.takenAt == nil || info.takenAt.Year() != 2020 || info.camera != "Phone" {
		t.Errorf("embedded exif: %+v", info)
	}
}

func TestParseVideoMeta(t *testing.T) {
	b, err := os.ReadFile("testdata/clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2022, 3, 15, 18, 0, 0, 0, time.UTC)
	if info := parseVideoMeta(b, int64(len(b)), nil); info.takenAt == nil || !info.takenAt.Equal(want) {
		t.Errorf("header moov: %+v", info)
	}
	// moov after a 300KB mdat: needs random access beyond the header.
	big := buildMP4(want, 300<<10, false)
	head := big[:headerBytes]
	if info := parseVideoMeta(head, int64(len(big)), nil); info.takenAt != nil {
		t.Error("tail moov without ReaderAt must be skipped")
	}
	if info := parseVideoMeta(head, int64(len(big)), bytes.NewReader(big)); info.takenAt == nil || !info.takenAt.Equal(want) {
		t.Errorf("tail moov via ReaderAt: %+v", info)
	}
}

func TestFilenameDate(t *testing.T) {
	cases := map[string]string{
		"IMG_20230105_101500.jpg":            "2023-01-05 10:15:00",
		"VID_20230105_235959.mp4":            "2023-01-05 23:59:59",
		"2023-01-05 trip.png":                "2023-01-05 00:00:00",
		"Screenshot_2019-12-31-08-00-01.png": "2019-12-31 08:00:01",
		"PXL_20240229.jpg":                   "2024-02-29 00:00:00",
		"IMG_20230230.jpg":                   "",
		"DSC01234.jpg":                       "",
		"file_120230105.jpg":                 "",
	}
	for name, want := range cases {
		got, ok := filenameDate(name)
		s := ""
		if ok {
			s = got.Format("2006-01-02 15:04:05")
		}
		if s != want {
			t.Errorf("%s: got %q, want %q", name, s, want)
		}
	}
}

func writeFile(t *testing.T, root, rel string, b []byte, mtime time.Time) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanMetadata_LocalFS(t *testing.T) {
	dirPath := t.TempDir()
	jpg, _ := os.ReadFile("testdata/exif.jpg")
	mtime := time.Date(2018, 5, 6, 7, 8, 9, 0, time.UTC)
	writeFile(t, dirPath, "photos/exif.jpg", jpg, time.Time{})
	writeFile(t, dirPath, "photos/IMG_20230105_101500.jpg", []byte("not a jpeg"), time.Time{})
	writeFile(t, dirPath, "photos/plain.png", []byte("not a png"), mtime)
	writeFile(t, dirPath, "videos/late.mov", buildMP4(time.Date(2022, 3, 15, 18, 0, 0, 0, time.UTC), 200<<10, false), time.Time{})
	writeFile(t, dirPath, "notes.txt", []byte("hi"), time.Time{})
	fs, err := remotefs.NewLocalFS(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := setupFileTestDB(t)
	_, session := scanSession(t, repo, fs, ".", func(s *db.ScanSession) { s.ExtractMetadata = true })

	check := func(p, source string, year int, camera string) {
		t.Helper()
		f := entry(t, repo, session.ID, p)
		if f.DateSource != source || (year != 0 && (f.TakenAt == nil || f.TakenAt.Year() != year)) || f.Camera != camera {
			t.Errorf("%s: source=%q taken=%v camera=%q, want %s %d %q", p, f.DateSource, f.TakenAt, f.Camera, source, year, camera)
		}
	}
	check("photos/exif.jpg", DateSourceExif, 2021, "TestCam X1")
	check("photos/IMG_20230105_101500.jpg", DateSourceFilename, 2023, "")
	check("photos/plain.png", DateSourceMtime, 2018, "")
	check("videos/late.mov", DateSourceVideo, 2022, "")
	check("notes.txt", "", 0, "")
	if f := entry(t, repo, session.ID, "photos/exif.jpg"); !f.HasGPS {
		t.Error("exif.jpg: HasGPS = false")
	}
}

func TestScanDuplicates_LocalFS(t *testing.T) {
	dirPath := t.TempDir()
	big := bytes.Repeat([]byte("0123456789abcdef"), 20<<10) // 320KB
	middle := bytes.Clone(big)
	middle[len(middle)/2] ^= 0xFF // same size, same ends: partial collision only
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	writeFile(t, dirPath, "b/deep/copy.bin", big, old)
	writeFile(t, dirPath, "a/copy2.bin", big, newer)
	writeFile(t, dirPath, "orig.bin", big, newer)
	writeFile(t, dirPath, "middle.bin", middle, old)
	writeFile(t, dirPath, "small1.txt", []byte("same"), old)
	writeFile(t, dirPath, "small2.txt", []byte("same"), newer)
	writeFile(t, dirPath, "other.txt", []byte("diff"), old)
	fs, err := remotefs.NewLocalFS(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := setupFileTestDB(t)
	_, session := scanSession(t, repo, fs, ".", func(s *db.ScanSession) { s.DetectDuplicates = true })

	orig := entry(t, repo, session.ID, "b/deep/copy.bin") // oldest wins over shorter paths
	if orig.DuplicateOf != nil || orig.Role != db.RoleUndecided || orig.Hash == "" {
		t.Errorf("original: dup=%v role=%q hash=%q", orig.DuplicateOf, orig.Role, orig.Hash)
	}
	for _, p := range []string{"a/copy2.bin", "orig.bin"} {
		f := entry(t, repo, session.ID, p)
		if f.DuplicateOf == nil || *f.DuplicateOf != orig.ID || f.Role != db.RoleDuplicate {
			t.Errorf("%s: dup=%v role=%q, want duplicate of %d", p, f.DuplicateOf, f.Role, orig.ID)
		}
	}
	if m := entry(t, repo, session.ID, "middle.bin"); m.DuplicateOf != nil || m.Hash == "" || m.Hash == orig.Hash {
		t.Errorf("middle.bin: dup=%v hash=%q (partial collision must be resolved by full hash)", m.DuplicateOf, m.Hash)
	}
	s1 := entry(t, repo, session.ID, "small1.txt")
	if s2 := entry(t, repo, session.ID, "small2.txt"); s2.DuplicateOf == nil || *s2.DuplicateOf != s1.ID {
		t.Errorf("small2.txt dup=%v, want %d", s2.DuplicateOf, s1.ID)
	}
	if o := entry(t, repo, session.ID, "other.txt"); o.DuplicateOf != nil {
		t.Error("other.txt flagged as duplicate")
	}

	session.DetectDuplicates = false
	if err := New(fs, repo, "", nil).Scan(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if f := entry(t, repo, session.ID, "orig.bin"); f.DuplicateOf != nil || f.Role != db.RoleUndecided || f.Hash != "" {
		t.Errorf("after disabling: dup=%v role=%q hash=%q", f.DuplicateOf, f.Role, f.Hash)
	}
}

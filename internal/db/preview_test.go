package db

import (
	"fmt"
	"testing"
)

// previewFixture: a project directory moved whole, loose photos spread over
// year folders, a kept file, and a name with LIKE wildcards.
//
//	proj/            → /代码/proj (whole)
//	  main.go (10)
//	  pkg/util.go (5)
//	photos/2023/a.jpg (100) → /照片/2023/a.jpg
//	photos/2023/b.jpg (200) → /照片/2023/b.jpg
//	photos/2024/c.jpg (300) → /照片/c.jpg
//	photos/keep.txt (1)      (kept)
//	odd_%/x.txt (7)          → /文档/odd_%/x.txt
func previewFixture(t *testing.T) *Repository {
	r := setupBrainRepo(t)
	seedPreviewFixture(t, r)
	return r
}

func seedPreviewFixture(t *testing.T, r *Repository) {
	t.Helper()
	for _, e := range []struct {
		p, typ string
		size   int64
	}{
		{"proj", "directory", 0}, {"proj/main.go", "file", 10}, {"proj/pkg", "directory", 0}, {"proj/pkg/util.go", "file", 5},
		{"photos", "directory", 0}, {"photos/2023", "directory", 0}, {"photos/2023/a.jpg", "file", 100},
		{"photos/2023/b.jpg", "file", 200}, {"photos/2024", "directory", 0}, {"photos/2024/c.jpg", "file", 300},
		{"photos/keep.txt", "file", 1}, {"odd_%", "directory", 0}, {"odd_%/x.txt", "file", 7},
	} {
		seedEntry(t, r, 1, e.p, e.typ, e.size)
	}
	for p, target := range map[string]string{
		"proj": "/代码/proj", "photos/2023/a.jpg": "/照片/2023/a.jpg", "photos/2023/b.jpg": "/照片/2023/b.jpg",
		"photos/2024/c.jpg": "/照片/c.jpg", "odd_%/x.txt": "/文档/odd_%/x.txt",
	} {
		typ := "file"
		if p == "proj" {
			typ = "directory"
		}
		if _, _, err := r.SetTargetUnique(1, p, target, typ); err != nil {
			t.Fatal(err)
		}
	}
}

func itemsByName(page *PreviewPage) map[string]PreviewItem {
	m := map[string]PreviewItem{}
	for _, it := range page.Items {
		m[it.Name] = it
	}
	return m
}

func TestPreviewAfterAggregates(t *testing.T) { checkPreviewAfter(t, previewFixture(t)) }

func checkPreviewAfter(t *testing.T, r *Repository) {
	t.Helper()
	root, err := r.PreviewAfter(1, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := itemsByName(root)
	if root.Total != 3 || len(got) != 3 {
		t.Fatalf("root must list 代码/照片/文档: %+v", root)
	}
	if it := got["照片"]; !it.IsDir || it.FileCount != 3 || it.Size != 600 || it.Path != "/照片" {
		t.Fatalf("照片 aggregate: %+v", it)
	}
	if it := got["代码"]; !it.IsDir || it.FileCount != 2 || it.Size != 15 {
		t.Fatalf("代码 must count the moved project's scanned files: %+v", it)
	}

	photos, _ := r.PreviewAfter(1, "/照片", 0, 0)
	pg := itemsByName(photos)
	if it := pg["2023"]; !it.IsDir || it.FileCount != 2 || it.FileID != 0 {
		t.Fatalf("2023 virtual dir: %+v", it)
	}
	if it := pg["c.jpg"]; it.IsDir || it.FileCount != 1 || it.FileID == 0 || it.OriginalPath != "photos/2024/c.jpg" {
		t.Fatalf("leaf file: %+v", it)
	}
	if photos.Items[0].Name != "2023" {
		t.Fatalf("directories first: %+v", photos.Items)
	}

	code, _ := r.PreviewAfter(1, "/代码", 0, 0)
	if it := itemsByName(code)["proj"]; !it.IsDir || it.FileID == 0 || it.OriginalPath != "proj" || it.FileCount != 2 {
		t.Fatalf("whole-moved directory leaf: %+v", it)
	}
	// Inside a directory moved whole, the listing maps back to real children.
	inside, _ := r.PreviewAfter(1, "/代码/proj", 0, 0)
	ig := itemsByName(inside)
	if inside.Total != 2 || ig["pkg"].Path != "/代码/proj/pkg" || !ig["pkg"].IsDir || ig["pkg"].FileCount != 1 || ig["main.go"].OriginalPath != "proj/main.go" {
		t.Fatalf("inside moved dir: %+v", inside)
	}
	deeper, _ := r.PreviewAfter(1, "/代码/proj/pkg", 0, 0)
	if deeper.Total != 1 || deeper.Items[0].Name != "util.go" {
		t.Fatalf("deeper inside moved dir: %+v", deeper)
	}

	// LIKE wildcards in path names match literally.
	docs, _ := r.PreviewAfter(1, "/文档/odd_%", 0, 0)
	if docs.Total != 1 || docs.Items[0].Name != "x.txt" {
		t.Fatalf("wildcard dir: %+v", docs)
	}
	if none, _ := r.PreviewAfter(1, "/文档/oddX%", 0, 0); none.Total != 0 {
		t.Fatalf("'_' must not match any char: %+v", none)
	}

	// Pagination.
	p1, _ := r.PreviewAfter(1, "", 0, 2)
	p2, _ := r.PreviewAfter(1, "", 2, 2)
	if len(p1.Items) != 2 || len(p2.Items) != 1 || p1.Total != 3 {
		t.Fatalf("pagination: %+v %+v", p1, p2)
	}
}

func TestPreviewBeforeStatuses(t *testing.T) { checkPreviewBefore(t, previewFixture(t)) }

func checkPreviewBefore(t *testing.T, r *Repository) {
	t.Helper()
	root, err := r.PreviewBefore(1, "", "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	g := itemsByName(root)
	if it := g["proj"]; it.Status != PreviewMoved || it.NewPath != "/代码/proj" || it.FileCount != 2 {
		t.Fatalf("proj: %+v", it)
	}
	if it := g["photos"]; it.Status != PreviewPartial || it.MovedCount != 3 || it.FileCount != 4 || it.Size != 601 {
		t.Fatalf("photos: %+v", it)
	}

	photos, _ := r.PreviewBefore(1, "", "photos", 0, 0)
	pg := itemsByName(photos)
	if pg["keep.txt"].Status != PreviewKept || pg["2023"].Status != PreviewPartial || pg["2023"].MovedCount != 2 {
		t.Fatalf("photos children: %+v", photos.Items)
	}

	// Under a directory moved whole, every child moves with it.
	inProj, _ := r.PreviewBefore(1, "", "proj/pkg", 0, 0)
	if inProj.Total != 1 || inProj.Items[0].Status != PreviewMoved || inProj.Items[0].NewPath != "/代码/proj/pkg/util.go" {
		t.Fatalf("inside moved dir: %+v", inProj)
	}
}

// The aggregation must stay a handful of queries regardless of size: a
// 5k-entry session lists its root without per-entry work.
func TestPreviewScales(t *testing.T) {
	r := setupBrainRepo(t)
	var rows []FileEntry
	for i := 0; i < 5000; i++ {
		p := fmt.Sprintf("in/d%03d/f%04d.txt", i%100, i)
		rows = append(rows, FileEntry{ScanSessionID: 1, OriginalPath: p, Name: fmt.Sprintf("f%04d.txt", i), FileType: "file",
			ParentPath: fmt.Sprintf("in/d%03d", i%100), Size: 1, NewPath: fmt.Sprintf("/文档/%d/f%04d.txt", i%10, i), Operation: "planned"})
	}
	if err := r.CreateFiles(rows); err != nil {
		t.Fatal(err)
	}
	page, err := r.PreviewAfter(1, "/文档", 0, 50)
	if err != nil || page.Total != 10 || page.Items[0].FileCount != 500 {
		t.Fatalf("scaled after: %+v %v", page, err)
	}
	before, err := r.PreviewBefore(1, "", "in/d001", 0, 20)
	if err != nil || before.Total != 50 || len(before.Items) != 20 || before.Items[0].Status != PreviewMoved {
		t.Fatalf("scaled before: %+v %v", before, err)
	}
}

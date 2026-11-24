package agent

import (
	"strings"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func setupGuardTest(t *testing.T) (*db.Repository, *ToolBuilder) {
	t.Helper()
	repo := setupToolTestRepo(t)
	tb := NewToolBuilder(repo, nil, 1, 7, NewLogger(1, 0, 0), config.AgentConfig{}, &db.ScanSession{AllowAutoCategory: true}, false)
	return repo, tb
}

func createCat(t *testing.T, tb *ToolBuilder, name, path string) map[string]any {
	t.Helper()
	return callTool(t, tb, ToolsetTagging, "create_category", map[string]string{"name": name, "path": path, "description": "测试"})
}

// primeGuard records the current category list like the shared context does.
func primeGuard(t *testing.T, repo *db.Repository, tb *ToolBuilder) {
	t.Helper()
	cats, err := repo.ListCategories(7)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(cats))
	for i, c := range cats {
		names[i] = c.Name
	}
	tb.guard.note(names)
}

func mustCreateCategory(t *testing.T, repo *db.Repository, fsID uint, name, path string) {
	t.Helper()
	if err := repo.CreateCategory(&db.Category{FilesystemID: fsID, Name: name, Path: path}); err != nil {
		t.Fatalf("seed category %s: %v", name, err)
	}
}

func TestCreateCategoryVersionGuard(t *testing.T) {
	repo, tb := setupGuardTest(t)

	out := createCat(t, tb, "音乐", "/music")
	if okOf(out) || !strings.Contains(errText(out), "尚未看到") {
		t.Fatalf("first create without snapshot: got %+v, want stale-version rejection", out)
	}
	if cats, _ := repo.ListCategories(7); len(cats) != 0 {
		t.Fatalf("rejected create must write nothing, got %d categories", len(cats))
	}

	mustOK(t, createCat(t, tb, "音乐", "/music"))
	mustOK(t, createCat(t, tb, "视频", "/videos"))

	mustCreateCategory(t, repo, 7, "文档", "/docs")
	out = createCat(t, tb, "图片", "/pictures")
	if okOf(out) || !strings.Contains(errText(out), "已变化") {
		t.Fatalf("create after external change: got %+v, want stale-version rejection", out)
	}
	if !strings.Contains(errText(out), "文档") || !strings.Contains(errText(out), "/docs") {
		t.Fatalf("rejection must include latest full list, got: %v", errText(out))
	}
	if _, err := repo.GetCategoryByName(7, "图片"); err == nil {
		t.Fatal("rejected create must write nothing")
	}

	mustOK(t, createCat(t, tb, "图片", "/pictures"))
}

func TestCreateCategoryRespectsSwitch(t *testing.T) {
	repo := setupToolTestRepo(t)
	tb := NewToolBuilder(repo, nil, 1, 7, NewLogger(1, 0, 0), config.AgentConfig{}, &db.ScanSession{AllowAutoCategory: false}, false)
	primeGuard(t, repo, tb)
	out := createCat(t, tb, "音乐", "/music")
	if okOf(out) || !strings.Contains(errText(out), "不能新建分类") {
		t.Fatalf("disabled switch must be a success:false result, got %+v", out)
	}
	if cats, _ := repo.ListCategories(7); len(cats) != 0 {
		t.Fatal("disabled create must write nothing")
	}
}

func TestCreateCategorySimilarityHardReject(t *testing.T) {
	repo, tb := setupGuardTest(t)

	mustCreateCategory(t, repo, 7, "Photos", "/photos")
	seedFile(t, repo, 1, "incoming/a.jpg", "file")
	if _, _, err := repo.SetTargetUnique(1, "incoming/a.jpg", "/photos/a.jpg", "file"); err != nil {
		t.Fatalf("set target: %v", err)
	}
	// Prime the version guard so only the similarity guard can fire.
	primeGuard(t, repo, tb)

	rejections := []struct {
		name    string
		catName string
		catPath string
		want    string
	}{
		{"normalized same name", "Photos", "/different", "名称相同"},
		{"case variant name", "photos", "/elsewhere", "名称相同"},
		{"exact same path", "新相册", "/photos", "路径相同"},
		{"normalized same path", "另一相册", "/PHOTOS", "路径相同"},
	}
	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			out := createCat(t, tb, tc.catName, tc.catPath)
			if okOf(out) {
				t.Fatalf("create %s %s: got success, want duplicate rejection", tc.catName, tc.catPath)
			}
			msg := errText(out)
			if !strings.Contains(msg, "拒绝创建") || !strings.Contains(msg, tc.want) {
				t.Fatalf("rejection lacks duplicate reference (%q): %v", tc.want, msg)
			}
			if !strings.Contains(msg, "Photos") || !strings.Contains(msg, "已归入 1 项") || !strings.Contains(out["hint"].(string), "decide") {
				t.Fatalf("rejection lacks name/file-count/decide reference: %v", out)
			}
		})
	}
	if cats, _ := repo.ListCategories(7); len(cats) != 1 {
		t.Fatalf("duplicate rejections must write nothing, got %d categories", len(cats))
	}
}

func TestCreateCategorySimilaritySoftReference(t *testing.T) {
	repo, tb := setupGuardTest(t)

	mustCreateCategory(t, repo, 7, "Photos", "/photos")
	mustCreateCategory(t, repo, 7, "工作文档", "/docs/work")
	seedFile(t, repo, 1, "incoming/a.jpg", "file")
	if _, _, err := repo.SetTargetUnique(1, "incoming/a.jpg", "/photos/a.jpg", "file"); err != nil {
		t.Fatalf("set target: %v", err)
	}
	primeGuard(t, repo, tb)

	similar := func(out map[string]any) []any {
		l, _ := out["similar_categories"].([]any)
		return l
	}
	first := func(l []any, k string) any { return l[0].(map[string]any)[k] }

	out := createCat(t, tb, "2025相册", "/photos/2025")
	mustOK(t, out)
	l := similar(out)
	if len(l) != 1 || first(l, "name") != "Photos" || first(l, "path") != "/photos" || first(l, "file_count") != float64(1) || first(l, "reason") == "" {
		t.Fatalf("contained create must carry similar reference: %+v", out)
	}
	if h, _ := out["hint"].(string); !strings.Contains(h, "decide") {
		t.Fatalf("hint must steer to a decide merge: %q", h)
	}
	if _, err := repo.GetCategoryByName(7, "2025相册"); err != nil {
		t.Fatalf("soft-similar category must exist in DB: %v", err)
	}

	out = createCat(t, tb, "文档", "/docs")
	mustOK(t, out)
	if l := similar(out); len(l) != 1 || first(l, "name") != "工作文档" {
		t.Fatalf("containing create must reference the contained category: %+v", out)
	}

	out = createCat(t, tb, "音乐", "/music")
	mustOK(t, out)
	if len(similar(out)) != 0 || out["hint"] != nil {
		t.Fatalf("novel create must carry no reference: %+v", out)
	}
	out = createCat(t, tb, "备份", "/photos2")
	mustOK(t, out)
	if len(similar(out)) != 0 {
		t.Fatalf("sibling prefix must not produce a reference: %+v", out)
	}
}

func TestCategoryFingerprint(t *testing.T) {
	base := categoryFingerprint([]string{"b", "a"})
	if base != categoryFingerprint([]string{"a", "b"}) {
		t.Fatal("fingerprint must be order-insensitive")
	}
	if base == categoryFingerprint([]string{"a"}) {
		t.Fatal("count change must change fingerprint")
	}
	if base == categoryFingerprint([]string{"a", "c"}) {
		t.Fatal("rename must change fingerprint")
	}
	if categoryFingerprint(nil) != categoryFingerprint([]string{}) {
		t.Fatal("nil and empty set must share a fingerprint")
	}
}

func TestUpdateCategoryClashIsRejection(t *testing.T) {
	repo, tb := setupGuardTest(t)
	for _, c := range []*db.Category{{FilesystemID: 7, Name: "照片", Path: "/照片", AgentEditable: true}, {FilesystemID: 7, Name: "相片", Path: "/相片", AgentEditable: true}} {
		if err := repo.CreateCategory(c); err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []map[string]string{{"name": "相片", "new_path": "/照片/"}, {"name": "相片", "new_name": "照片"}} {
		out := callTool(t, tb, ToolsetMain, "update_category", in)
		if okOf(out) || !strings.Contains(errText(out), "已被分类「照片」使用") {
			t.Fatalf("clash %+v: got %+v — want a tool rejection", in, out)
		}
	}
	mustOK(t, callTool(t, tb, ToolsetMain, "update_category", map[string]string{"name": "相片", "new_path": "/相片", "description": "手机照片"}))
}

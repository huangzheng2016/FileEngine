package agent

import (
	"context"
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

func mustCreateCategory(t *testing.T, repo *db.Repository, fsID uint, name, path string) {
	t.Helper()
	if err := repo.CreateCategory(&db.Category{FilesystemID: fsID, Name: name, Path: path}); err != nil {
		t.Fatalf("seed category %s: %v", name, err)
	}
}

func TestCreateCategoryVersionGuard(t *testing.T) {
	repo, tb := setupGuardTest(t)
	ctx := context.Background()

	out, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "音乐", Path: "/music"})
	if err != nil {
		t.Fatalf("guard rejection must not abort the run: %v", err)
	}
	if out.Success || !strings.Contains(out.Error, "尚未查看") {
		t.Fatalf("first create without snapshot: got %+v, want stale-version rejection", out)
	}
	if cats, _ := repo.ListCategories(7); len(cats) != 0 {
		t.Fatalf("rejected create must write nothing, got %d categories", len(cats))
	}

	if _, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "音乐", Path: "/music"}); err != nil {
		t.Fatalf("retry after rejection: %v", err)
	}
	if _, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "视频", Path: "/videos"}); err != nil {
		t.Fatalf("back-to-back create after own creation: %v", err)
	}

	mustCreateCategory(t, repo, 7, "文档", "/docs")
	out, err = tb.createCategory(ctx, &CreateCategoryInput{Name: "图片", Path: "/pictures"})
	if err != nil {
		t.Fatalf("guard rejection must not abort the run: %v", err)
	}
	if out.Success || !strings.Contains(out.Error, "已变化") {
		t.Fatalf("create after external change: got %+v, want stale-version rejection", out)
	}
	if !strings.Contains(out.Error, "文档") || !strings.Contains(out.Error, "/docs") {
		t.Fatalf("rejection must include latest full list, got: %v", out.Error)
	}
	if _, err := repo.GetCategoryByName(7, "图片"); err == nil {
		t.Fatal("rejected create must write nothing")
	}

	if _, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "图片", Path: "/pictures"}); err != nil {
		t.Fatalf("retry after re-read: %v", err)
	}
}

func TestListCategoriesPrimesVersionGuard(t *testing.T) {
	_, tb := setupGuardTest(t)
	ctx := context.Background()

	if _, err := tb.listCategories(ctx, &ListCategoriesInput{}); err != nil {
		t.Fatalf("listCategories: %v", err)
	}
	if _, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "音乐", Path: "/music"}); err != nil {
		t.Fatalf("create right after list_categories must pass the version guard: %v", err)
	}
}

func TestCreateCategorySimilarityHardReject(t *testing.T) {
	repo, tb := setupGuardTest(t)
	ctx := context.Background()

	mustCreateCategory(t, repo, 7, "Photos", "/photos")
	seedFile(t, repo, 1, "incoming/a.jpg", "file")
	if _, _, err := repo.SetTargetUnique(1, "incoming/a.jpg", "/photos/a.jpg", "file"); err != nil {
		t.Fatalf("set target: %v", err)
	}
	// Prime the version guard so only the similarity guard can fire.
	if _, err := tb.listCategories(ctx, &ListCategoriesInput{}); err != nil {
		t.Fatalf("listCategories: %v", err)
	}

	rejections := []struct {
		name string
		in   CreateCategoryInput
		want string
	}{
		{"normalized same name", CreateCategoryInput{Name: "Photos", Path: "/different"}, "名称相同"},
		{"case variant name", CreateCategoryInput{Name: "photos", Path: "/elsewhere"}, "名称相同"},
		{"exact same path", CreateCategoryInput{Name: "新相册", Path: "/photos"}, "路径相同"},
		{"normalized same path", CreateCategoryInput{Name: "另一相册", Path: "/PHOTOS"}, "路径相同"},
	}
	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tb.createCategory(ctx, &tc.in)
			if err != nil {
				t.Fatalf("duplicate rejection must not abort the run: %v", err)
			}
			if out.Success {
				t.Fatalf("create %+v: got success, want duplicate rejection", tc.in)
			}
			if !strings.Contains(out.Error, "拒绝创建") || !strings.Contains(out.Error, tc.want) {
				t.Fatalf("rejection lacks duplicate reference (%q): %v", tc.want, out.Error)
			}
			if !strings.Contains(out.Error, "Photos") || !strings.Contains(out.Error, "已归入 1 项") || !strings.Contains(out.Error, "set_target") {
				t.Fatalf("rejection lacks name/file-count/set_target reference: %v", out.Error)
			}
		})
	}
	if cats, _ := repo.ListCategories(7); len(cats) != 1 {
		t.Fatalf("duplicate rejections must write nothing, got %d categories", len(cats))
	}
}

func TestCreateCategorySimilaritySoftReference(t *testing.T) {
	repo, tb := setupGuardTest(t)
	ctx := context.Background()

	mustCreateCategory(t, repo, 7, "Photos", "/photos")
	mustCreateCategory(t, repo, 7, "工作文档", "/docs/work")
	seedFile(t, repo, 1, "incoming/a.jpg", "file")
	if _, _, err := repo.SetTargetUnique(1, "incoming/a.jpg", "/photos/a.jpg", "file"); err != nil {
		t.Fatalf("set target: %v", err)
	}
	if _, err := tb.listCategories(ctx, &ListCategoriesInput{}); err != nil {
		t.Fatalf("listCategories: %v", err)
	}

	out, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "2025相册", Path: "/photos/2025"})
	if err != nil {
		t.Fatalf("contained path must be created, got rejection: %v", err)
	}
	if len(out.SimilarCategories) != 1 || out.SimilarCategories[0].Name != "Photos" ||
		out.SimilarCategories[0].Path != "/photos" || out.SimilarCategories[0].FileCount != 1 ||
		out.SimilarCategories[0].Reason == "" {
		t.Fatalf("contained create must carry similar reference: %+v", out.SimilarCategories)
	}
	if !strings.Contains(out.Guidance, "set_targets") {
		t.Fatalf("guidance must steer to a set_targets merge: %q", out.Guidance)
	}
	if _, err := repo.GetCategoryByName(7, "2025相册"); err != nil {
		t.Fatalf("soft-similar category must exist in DB: %v", err)
	}

	out, err = tb.createCategory(ctx, &CreateCategoryInput{Name: "文档", Path: "/docs"})
	if err != nil {
		t.Fatalf("containing path must be created, got rejection: %v", err)
	}
	if len(out.SimilarCategories) != 1 || out.SimilarCategories[0].Name != "工作文档" {
		t.Fatalf("containing create must reference the contained category: %+v", out.SimilarCategories)
	}

	out, err = tb.createCategory(ctx, &CreateCategoryInput{Name: "音乐", Path: "/music"})
	if err != nil {
		t.Fatalf("novel category: %v", err)
	}
	if len(out.SimilarCategories) != 0 || out.Guidance != "" {
		t.Fatalf("novel create must carry no reference: %+v", out)
	}
	out, err = tb.createCategory(ctx, &CreateCategoryInput{Name: "备份", Path: "/photos2"})
	if err != nil {
		t.Fatalf("sibling-prefix path must not count as overlap: %v", err)
	}
	if len(out.SimilarCategories) != 0 {
		t.Fatalf("sibling prefix must not produce a reference: %+v", out.SimilarCategories)
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
	ctx := context.Background()
	for _, c := range []*db.Category{{FilesystemID: 7, Name: "照片", Path: "/照片", AgentEditable: true}, {FilesystemID: 7, Name: "相片", Path: "/相片", AgentEditable: true}} {
		if err := repo.CreateCategory(c); err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []UpdateCategoryInput{{Name: "相片", NewPath: "/照片/"}, {Name: "相片", NewName: "照片"}} {
		out, err := tb.updateCategory(ctx, &in)
		if err != nil || out.Success || !strings.Contains(out.Error, "已被分类「照片」使用") {
			t.Fatalf("clash %+v: got %+v, %v — want a tool rejection", in, out, err)
		}
	}
	if out, err := tb.updateCategory(ctx, &UpdateCategoryInput{Name: "相片", NewPath: "/相片", Description: "手机照片"}); err != nil || !out.Success {
		t.Fatalf("keeping its own path is no clash: %+v, %v", out, err)
	}
}

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
	tb := NewToolBuilder(repo, nil, 1, 7, NewLogger(nil, 1), config.AgentConfig{}, &db.ScanSession{AllowAutoCategory: true}, false)
	return repo, tb
}

func mustCreateCategory(t *testing.T, repo *db.Repository, fsID uint, name, path string) {
	t.Helper()
	if err := repo.CreateCategory(&db.Category{FilesystemID: fsID, Name: name, Path: path}); err != nil {
		t.Fatalf("seed category %s: %v", name, err)
	}
}

// Version guard: create_category is rejected while the agent holds a stale
// (or no) category snapshot, the rejection carries the latest complete list,
// and a retry after that re-read passes. The agent's own successful creation
// must not re-trip the guard.
func TestCreateCategoryVersionGuard(t *testing.T) {
	repo, tb := setupGuardTest(t)
	ctx := context.Background()

	// Never saw any list -> rejected, and the rejection shows the current
	// (here: empty) state plus instructions.
	_, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "音乐", Path: "/music"})
	if err == nil || !strings.Contains(err.Error(), "尚未查看") {
		t.Fatalf("first create without snapshot: got %v, want stale-version rejection", err)
	}
	if cats, _ := repo.ListCategories(7); len(cats) != 0 {
		t.Fatalf("rejected create must write nothing, got %d categories", len(cats))
	}

	// The rejection recorded the fresh snapshot, so the immediate retry based
	// on it succeeds...
	if _, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "音乐", Path: "/music"}); err != nil {
		t.Fatalf("retry after rejection: %v", err)
	}
	// ...and the agent's own creation does not re-trip the guard.
	if _, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "视频", Path: "/videos"}); err != nil {
		t.Fatalf("back-to-back create after own creation: %v", err)
	}

	// An external change (another worker / the user) invalidates the snapshot.
	mustCreateCategory(t, repo, 7, "文档", "/docs")
	_, err = tb.createCategory(ctx, &CreateCategoryInput{Name: "图片", Path: "/pictures"})
	if err == nil || !strings.Contains(err.Error(), "已变化") {
		t.Fatalf("create after external change: got %v, want stale-version rejection", err)
	}
	// The rejection must carry the latest complete list — including the
	// externally added category — so the agent can re-decide from it.
	if !strings.Contains(err.Error(), "文档") || !strings.Contains(err.Error(), "/docs") {
		t.Fatalf("rejection must include latest full list, got: %v", err)
	}
	if _, err := repo.GetCategoryByName(7, "图片"); err == nil {
		t.Fatal("rejected create must write nothing")
	}

	// Retry after the forced re-read passes.
	if _, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "图片", Path: "/pictures"}); err != nil {
		t.Fatalf("retry after re-read: %v", err)
	}
}

// list_categories is an observation point too: after calling it, creation
// proceeds without a version challenge.
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

// Similarity guard: normalized duplicate names, case variants and path-prefix
// overlaps are rejected with reference info (similar name/path/file count)
// steering to set_target; a truly novel category is untouched.
func TestCreateCategorySimilarityGuard(t *testing.T) {
	repo, tb := setupGuardTest(t)
	ctx := context.Background()

	mustCreateCategory(t, repo, 7, "Photos", "/photos")
	// One planned file under /photos so the reference info has a real count.
	seedFile(t, repo, 1, "incoming/a.jpg", "file")
	if err := repo.SetTargetAtomic(1, "incoming/a.jpg", "/photos/a.jpg", "planned", "file"); err != nil {
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
		{"normalized same name", CreateCategoryInput{Name: "Photos", Path: "/different"}, "Photos"},
		{"case variant", CreateCategoryInput{Name: "photos", Path: "/elsewhere"}, "Photos"},
		{"new path under existing", CreateCategoryInput{Name: "2024相册", Path: "/photos/2024"}, "/photos"},
		{"existing path under new", CreateCategoryInput{Name: "图片库", Path: "/photos"}, "/photos"},
	}
	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tb.createCategory(ctx, &tc.in)
			if err == nil {
				t.Fatalf("create %+v: got nil, want similarity rejection", tc.in)
			}
			if !strings.Contains(err.Error(), "相似分类") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("rejection lacks reference info (%q): %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), "已归入 1 项") || !strings.Contains(err.Error(), "set_target") {
				t.Fatalf("rejection lacks file count / set_target guidance: %v", err)
			}
		})
	}
	if cats, _ := repo.ListCategories(7); len(cats) != 1 {
		t.Fatalf("similarity rejections must write nothing, got %d categories", len(cats))
	}

	// Sibling prefix that only shares bytes ("photos2" vs "photos") and a
	// genuinely new name/path are novel — both pass.
	if _, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "音乐", Path: "/music"}); err != nil {
		t.Fatalf("novel category: %v", err)
	}
	if _, err := tb.createCategory(ctx, &CreateCategoryInput{Name: "备份", Path: "/photos2"}); err != nil {
		t.Fatalf("sibling-prefix path must not count as overlap: %v", err)
	}
}

// The version fingerprint is the count plus the name set: renames, creations
// and deletions all change it.
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

package agent

import (
	"context"
	"path/filepath"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func setupToolTestRepo(t *testing.T) *db.Repository {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := db.Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("db init: %v", err)
	}
	return db.NewRepository(db.GetDB())
}

func seedFile(t *testing.T, repo *db.Repository, sessionID uint, path, fileType string) {
	t.Helper()
	f := &db.FileEntry{
		ScanSessionID: sessionID,
		OriginalPath:  path,
		Name:          filepath.Base(path),
		FileType:      fileType,
	}
	if err := repo.CreateFile(f); err != nil {
		t.Fatalf("seed file %s: %v", path, err)
	}
}

func seedCategory(t *testing.T, repo *db.Repository, filesystemID uint, name, path string) {
	t.Helper()
	if err := repo.CreateCategory(&db.Category{FilesystemID: filesystemID, Name: name, Path: path}); err != nil {
		t.Fatalf("seed category %s: %v", name, err)
	}
}

// Category paths become new_path prefixes, so they follow the same rules.
func TestCategoryPathValidation(t *testing.T) {
	repo := setupToolTestRepo(t)
	tb := NewToolBuilder(repo, nil, 1, 7, NewLogger(1, 0, 0), config.AgentConfig{}, &db.ScanSession{AllowAutoCategory: true}, false)

	if out, err := tb.createCategory(context.Background(), &CreateCategoryInput{Name: "Bad", Path: "/../evil"}); err != nil || out.Success {
		t.Fatalf("createCategory with escaping path: got %+v, %v — want non-fatal rejection", out, err)
	}
	if out, err := tb.createCategory(context.Background(), &CreateCategoryInput{Name: "Empty", Path: ""}); err != nil || out.Success {
		t.Fatalf("createCategory with empty path: got %+v, %v — want non-fatal rejection", out, err)
	}
	// The version guard only lets create_category run against a list the agent
	// has just seen — prime it via list_categories.
	if _, err := tb.listCategories(context.Background(), &ListCategoriesInput{}); err != nil {
		t.Fatalf("listCategories: %v", err)
	}
	if _, err := tb.createCategory(context.Background(), &CreateCategoryInput{Name: "Good", Path: "/organized/good"}); err != nil {
		t.Fatalf("createCategory valid: %v", err)
	}

	if out, err := tb.updateCategory(context.Background(), &UpdateCategoryInput{Name: "Good", NewPath: "/a/../../b"}); err != nil || out.Success {
		t.Fatalf("updateCategory with escaping new_path: got %+v, %v — want non-fatal rejection", out, err)
	}
	cat, err := repo.GetCategoryByName(7, "Good")
	if err != nil {
		t.Fatalf("reload category: %v", err)
	}
	if cat.Path != "/organized/good" {
		t.Fatalf("rejected update must not change path: got %q", cat.Path)
	}
	if _, err := tb.updateCategory(context.Background(), &UpdateCategoryInput{Name: "Good", NewPath: "/organized/better"}); err != nil {
		t.Fatalf("updateCategory valid: %v", err)
	}
}

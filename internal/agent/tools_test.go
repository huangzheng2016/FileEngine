package agent

import (
	"path/filepath"
	"testing"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func setupToolTestRepo(t *testing.T) *db.Repository {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := db.Open(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("db init: %v", err)
	}
	return db.NewRepository(dal.DB())
}

func seedFile(t *testing.T, repo *db.Repository, sessionID uint, path, fileType string) {
	t.Helper()
	f := &model.FileEntry{
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
	if err := repo.CreateCategory(&model.Category{FilesystemID: filesystemID, Name: name, Path: path}); err != nil {
		t.Fatalf("seed category %s: %v", name, err)
	}
}

func TestCategoryPathValidation(t *testing.T) {
	repo := setupToolTestRepo(t)
	tb := NewToolBuilder(repo, nil, 1, 7, NewLogger(1, 0, 0), config.AgentConfig{}, &model.ScanSession{AllowAutoCategory: true}, false)

	if out := createCat(t, tb, "Bad", "/../evil"); okOf(out) {
		t.Fatalf("create with escaping path: got %+v — want non-fatal rejection", out)
	}
	if out := createCat(t, tb, "Empty", ""); okOf(out) {
		t.Fatalf("create with empty path: got %+v — want non-fatal rejection", out)
	}
	// The version guard only lets create_category run against a list the agent
	// has just seen.
	primeGuard(t, repo, tb)
	mustOK(t, createCat(t, tb, "Good", "/organized/good"))

	if out := callTool(t, tb, ToolsetMain, "update_category", map[string]string{"name": "Good", "new_path": "/a/../../b"}); okOf(out) {
		t.Fatalf("update with escaping new_path: got %+v — want non-fatal rejection", out)
	}
	cat, err := repo.GetCategoryByName(7, "Good")
	if err != nil {
		t.Fatalf("reload category: %v", err)
	}
	if cat.Path != "/organized/good" {
		t.Fatalf("rejected update must not change path: got %q", cat.Path)
	}
	mustOK(t, callTool(t, tb, ToolsetMain, "update_category", map[string]string{"name": "Good", "new_path": "/organized/better"}))
}

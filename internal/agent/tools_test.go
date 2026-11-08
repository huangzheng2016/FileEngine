package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func TestValidateFSPath(t *testing.T) {
	valid := []string{
		"", // set_target: empty clears the target
		"/organized/photos",
		"organized/photos",
		"/a/b/c.txt",
		"/中文 分类/相册 2024",
		"/a.b/c-d_e(f)",
		"relative/dir",
	}
	for _, p := range valid {
		if err := ValidateFSPath(p); err != nil {
			t.Fatalf("ValidateFSPath(%q): got error %v, want nil", p, err)
		}
	}

	invalid := []string{
		"/",         // bare root names nothing
		".",         // ditto
		"..",        // escapes root
		"../x",      // escapes root
		"/..",       // escapes root (host-absolute on SFTP-style backends)
		"/a/../b",   // '..' segment
		"a/../../b", // escapes root
		"//../x",    // still contains a '..' segment
		"a/b/../../../c",
	}
	for _, p := range invalid {
		if err := ValidateFSPath(p); err == nil {
			t.Fatalf("ValidateFSPath(%q): got nil, want error", p)
		}
	}

	if err := ValidateFSPath("a\x00b"); err == nil {
		t.Fatal("ValidateFSPath with NUL byte: got nil, want error")
	}
}

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

// set_target must reject illegal targets loudly and write nothing.
func TestSetTargetRejectsIllegalPaths(t *testing.T) {
	repo := setupToolTestRepo(t)
	seedFile(t, repo, 1, "incoming/photo.jpg", "file")

	tb := NewToolBuilder(repo, nil, 1, 1, NewLogger(1, 0, 0), config.AgentConfig{}, &db.ScanSession{}, false)

	bad := []string{"/", "..", "../escape", "/../etc/passwd", "/organized/../../escape"}
	for _, p := range bad {
		out, err := tb.setTarget(context.Background(), &SetTargetInput{Path: "incoming/photo.jpg", NewPath: p})
		if err != nil {
			t.Fatalf("setTarget new_path=%q: rejection must be a non-fatal result, got run error: %v", p, err)
		}
		if out.Success || !strings.Contains(out.Error, "invalid new_path") {
			t.Fatalf("setTarget new_path=%q: got %+v, want invalid-path rejection", p, out)
		}
	}

	f, err := repo.GetFileByPath(1, "incoming/photo.jpg")
	if err != nil {
		t.Fatalf("reload file: %v", err)
	}
	if f.NewPath != "" || f.Operation != "" {
		t.Fatalf("illegal target must not be written: new_path=%q operation=%q", f.NewPath, f.Operation)
	}
}

// set_target accepts legal targets (writes them) and clears them on empty.
func TestSetTargetValidAndClear(t *testing.T) {
	repo := setupToolTestRepo(t)
	seedFile(t, repo, 1, "incoming/photo.jpg", "file")

	tb := NewToolBuilder(repo, nil, 1, 1, NewLogger(1, 0, 0), config.AgentConfig{}, &db.ScanSession{}, false)

	if _, err := tb.setTarget(context.Background(), &SetTargetInput{Path: "incoming/photo.jpg", NewPath: "/organized/photos/photo.jpg"}); err != nil {
		t.Fatalf("setTarget valid: %v", err)
	}
	f, _ := repo.GetFileByPath(1, "incoming/photo.jpg")
	if f.NewPath != "/organized/photos/photo.jpg" || f.Operation != "planned" {
		t.Fatalf("after set: new_path=%q operation=%q", f.NewPath, f.Operation)
	}

	// Empty new_path clears the target.
	if _, err := tb.setTarget(context.Background(), &SetTargetInput{Path: "incoming/photo.jpg", NewPath: ""}); err != nil {
		t.Fatalf("setTarget clear: %v", err)
	}
	f, _ = repo.GetFileByPath(1, "incoming/photo.jpg")
	if f.NewPath != "" || f.Operation != "" {
		t.Fatalf("after clear: new_path=%q operation=%q", f.NewPath, f.Operation)
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

// set_target on a directory cascade-clears planned children — the tool result
// must surface that consequence (cleared_children + guidance) so the agent
// can re-decide instead of silently discarding finer classifications.
func TestSetTargetCascadeClearWarning(t *testing.T) {
	repo := setupToolTestRepo(t)
	seedFile(t, repo, 1, "db", "directory")
	seedFile(t, repo, 1, "db/azure", "directory")
	tb := NewToolBuilder(repo, nil, 1, 1, NewLogger(1, 0, 0), config.AgentConfig{}, &db.ScanSession{}, false)

	if _, err := tb.setTarget(context.Background(), &SetTargetInput{Path: "db/azure", NewPath: "/backups/database/azure"}); err != nil {
		t.Fatalf("child set: %v", err)
	}
	out, err := tb.setTarget(context.Background(), &SetTargetInput{Path: "db", NewPath: "/backups/database"})
	if err != nil {
		t.Fatalf("parent set: %v", err)
	}
	if out.ClearedChildren != 1 || !strings.Contains(out.Guidance, "覆盖") {
		t.Fatalf("cascade clear must be reported: %+v", out)
	}
	f, _ := repo.GetFileByPath(1, "db/azure")
	if f.NewPath != "" {
		t.Fatalf("child target must be cascade-cleared, got %q", f.NewPath)
	}
}

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
		if err := validateFSPath(p); err != nil {
			t.Fatalf("validateFSPath(%q): got error %v, want nil", p, err)
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
		if err := validateFSPath(p); err == nil {
			t.Fatalf("validateFSPath(%q): got nil, want error", p)
		}
	}

	if err := validateFSPath("a\x00b"); err == nil {
		t.Fatal("validateFSPath with NUL byte: got nil, want error")
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

	tb := NewToolBuilder(repo, nil, 1, 1, NewLogger(nil, 1), config.AgentConfig{}, &db.ScanSession{}, false)

	bad := []string{"/", "..", "../escape", "/../etc/passwd", "/organized/../../escape"}
	for _, p := range bad {
		_, err := tb.setTarget(context.Background(), &SetTargetInput{Path: "incoming/photo.jpg", NewPath: p})
		if err == nil {
			t.Fatalf("setTarget new_path=%q: got nil error, want rejection", p)
		}
		if !strings.Contains(err.Error(), "invalid new_path") {
			t.Fatalf("setTarget new_path=%q: error %v lacks 'invalid new_path'", p, err)
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

	tb := NewToolBuilder(repo, nil, 1, 1, NewLogger(nil, 1), config.AgentConfig{}, &db.ScanSession{}, false)

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
	tb := NewToolBuilder(repo, nil, 1, 7, NewLogger(nil, 1), config.AgentConfig{}, &db.ScanSession{AllowAutoCategory: true}, false)

	if _, err := tb.createCategory(context.Background(), &CreateCategoryInput{Name: "Bad", Path: "/../evil"}); err == nil {
		t.Fatal("createCategory with escaping path: got nil error, want rejection")
	}
	if _, err := tb.createCategory(context.Background(), &CreateCategoryInput{Name: "Empty", Path: ""}); err == nil {
		t.Fatal("createCategory with empty path: got nil error, want rejection")
	}
	if _, err := tb.createCategory(context.Background(), &CreateCategoryInput{Name: "Good", Path: "/organized/good"}); err != nil {
		t.Fatalf("createCategory valid: %v", err)
	}

	if _, err := tb.updateCategory(context.Background(), &UpdateCategoryInput{Name: "Good", NewPath: "/a/../../b"}); err == nil {
		t.Fatal("updateCategory with escaping new_path: got nil error, want rejection")
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

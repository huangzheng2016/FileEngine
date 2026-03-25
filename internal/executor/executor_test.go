package executor

import (
	"context"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *db.Repository {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gdb.AutoMigrate(&db.FileEntry{}, &db.ScanSession{}, &db.Category{}, &db.Filesystem{}, &db.AgentLog{}, &db.ModelProvider{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db.NewRepository(gdb)
}

func TestExecutor_CopyDir_SMB(t *testing.T) {
	repo := setupTestDB(t)
	fs, err := remotefs.NewSMBFS(config.RemoteFSConfig{
		Host: "hz2016-qnasmini", Username: "test", Password: "test123", BasePath: "test",
	})
	if err != nil {
		t.Fatalf("NewSMBFS: %v", err)
	}
	defer fs.Close()

	ctx := context.Background()

	// List root to find a directory to copy
	entries, err := fs.List(ctx, ".")
	if err != nil {
		t.Fatalf("List root: %v", err)
	}

	var srcDir string
	for _, e := range entries {
		if e.IsDir {
			srcDir = e.Name
			break
		}
	}
	if srcDir == "" {
		t.Skip("no directory found in test share to copy")
	}

	dstDir := "_test_copy_" + srcDir
	t.Logf("Copying %s -> %s", srcDir, dstDir)

	// Test recursive copy
	exec := New(repo, fs)
	err = exec.recursiveCopy(ctx, srcDir, dstDir)
	if err != nil {
		t.Fatalf("recursiveCopy: %v", err)
	}

	// Verify destination exists and has entries
	dstEntries, err := fs.List(ctx, dstDir)
	if err != nil {
		t.Fatalf("List dst: %v", err)
	}
	t.Logf("Destination %s has %d entries", dstDir, len(dstEntries))
	if len(dstEntries) == 0 {
		t.Error("expected entries in copied directory")
	}

	// Cleanup: we can't easily delete recursively, just log
	t.Logf("NOTE: test directory %s left on share for manual cleanup", dstDir)
}

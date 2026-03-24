package scanner

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
	if err := gdb.AutoMigrate(&db.FileEntry{}, &db.ScanSession{}, &db.Category{}, &db.Filesystem{}, &db.AgentLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db.NewRepository(gdb)
}

func TestScan_SMB(t *testing.T) {
	repo := setupTestDB(t)
	fs, err := remotefs.NewSMBFS(config.RemoteFSConfig{
		Host: "hz2016-qnasmini", Username: "test", Password: "test123", BasePath: "test",
	})
	if err != nil {
		t.Fatalf("NewSMBFS: %v", err)
	}
	defer fs.Close()

	session := &db.ScanSession{ID: 1, RootPath: ".", Status: "scanning"}
	repo.CreateSession(session)

	sc := New(fs, repo)
	if err := sc.Scan(context.Background(), session); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	t.Logf("Status: %s, TotalFiles: %d", session.Status, session.TotalFiles)
	if session.Status != "scanned" {
		t.Errorf("expected status=scanned, got %s", session.Status)
	}
	if session.TotalFiles < 100 {
		t.Errorf("expected TotalFiles >= 100, got %d", session.TotalFiles)
	}
}

func TestScan_FTP(t *testing.T) {
	repo := setupTestDB(t)
	fs, err := remotefs.NewFTPFS(config.RemoteFSConfig{
		Host: "hz2016-qnasmini", Username: "test", Password: "test123", BasePath: "/test",
	})
	if err != nil {
		t.Fatalf("NewFTPFS: %v", err)
	}
	defer fs.Close()

	session := &db.ScanSession{ID: 1, RootPath: ".", Status: "scanning"}
	repo.CreateSession(session)

	sc := New(fs, repo)
	if err := sc.Scan(context.Background(), session); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	t.Logf("Status: %s, TotalFiles: %d", session.Status, session.TotalFiles)
	if session.Status != "scanned" {
		t.Errorf("expected status=scanned, got %s", session.Status)
	}
	if session.TotalFiles == 0 {
		t.Error("expected TotalFiles > 0")
	}
}

func TestScan_SFTP(t *testing.T) {
	repo := setupTestDB(t)
	fs, err := remotefs.NewSFTPFS(config.RemoteFSConfig{
		Host: "hz2016-qnasmini", Username: "test", Password: "test123", BasePath: "/vol1/1001/test",
	})
	if err != nil {
		t.Fatalf("NewSFTPFS: %v", err)
	}
	defer fs.Close()

	session := &db.ScanSession{ID: 1, RootPath: ".", Status: "scanning"}
	repo.CreateSession(session)

	sc := New(fs, repo)
	if err := sc.Scan(context.Background(), session); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	t.Logf("Status: %s, TotalFiles: %d", session.Status, session.TotalFiles)
	if session.Status != "scanned" {
		t.Errorf("expected status=scanned, got %s", session.Status)
	}
	if session.TotalFiles == 0 {
		t.Error("expected TotalFiles > 0")
	}
}

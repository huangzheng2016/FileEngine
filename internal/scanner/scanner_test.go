package scanner

import (
	"context"
	"net"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testNASHost is the env-gated test NAS used by the integration tests below;
// requireTestNAS skips them off that network. RFC 2606 .invalid — no real
// hosts in the repo (credentials hygiene).
const testNASHost = "test-nas.invalid"

func requireTestNAS(t *testing.T) {
	t.Helper()
	if _, err := net.LookupHost(testNASHost); err != nil {
		t.Skipf("test NAS %q not resolvable on this network: %v", testNASHost, err)
	}
}

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
	requireTestNAS(t)
	repo := setupTestDB(t)
	fs, err := remotefs.NewSMBFS(config.RemoteFSConfig{
		Host: testNASHost, Username: "test", Password: "not-a-real-password", BasePath: "test",
	})
	if err != nil {
		t.Fatalf("NewSMBFS: %v", err)
	}
	defer fs.Close()

	session := &db.ScanSession{ID: 1, RootPath: ".", Status: "scanning"}
	repo.CreateSession(session)

	sc := New(fs, repo, "", nil)
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
	requireTestNAS(t)
	repo := setupTestDB(t)
	fs, err := remotefs.NewFTPFS(config.RemoteFSConfig{
		Host: testNASHost, Username: "test", Password: "not-a-real-password", BasePath: "/test",
	})
	if err != nil {
		t.Fatalf("NewFTPFS: %v", err)
	}
	defer fs.Close()

	session := &db.ScanSession{ID: 1, RootPath: ".", Status: "scanning"}
	repo.CreateSession(session)

	sc := New(fs, repo, "", nil)
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
	requireTestNAS(t)
	repo := setupTestDB(t)
	fs, err := remotefs.NewSFTPFS(config.RemoteFSConfig{
		Host: testNASHost, Username: "test", Password: "not-a-real-password", BasePath: "/vol1/1001/test",
	})
	if err != nil {
		t.Fatalf("NewSFTPFS: %v", err)
	}
	defer fs.Close()

	session := &db.ScanSession{ID: 1, RootPath: ".", Status: "scanning"}
	repo.CreateSession(session)

	sc := New(fs, repo, "", nil)
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

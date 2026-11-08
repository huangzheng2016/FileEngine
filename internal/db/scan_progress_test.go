package db

import (
	"path/filepath"
	"testing"

	"FileEngine/internal/config"
)

func setupScanProgressRepo(t *testing.T) *Repository {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	return NewRepository(DB)
}

func TestUpdateScanProgress(t *testing.T) {
	r := setupScanProgressRepo(t)
	session := &ScanSession{FilesystemID: 1, Status: "scanning", TotalFiles: 7}
	if err := r.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := r.UpdateScanProgress(session.ID, 1234, "photos/raw"); err != nil {
		t.Fatalf("UpdateScanProgress: %v", err)
	}

	got, err := r.GetSession(session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.ScannedCount != 1234 || got.CurrentPath != "photos/raw" {
		t.Errorf("progress = (%d, %q), want (1234, photos/raw)", got.ScannedCount, got.CurrentPath)
	}
	if got.Status != "scanning" || got.TotalFiles != 7 {
		t.Errorf("unrelated columns clobbered: status=%s total_files=%d", got.Status, got.TotalFiles)
	}

	if err := r.UpdateScanProgress(session.ID, 2000, ""); err != nil {
		t.Fatalf("UpdateScanProgress clear: %v", err)
	}
	got, _ = r.GetSession(session.ID)
	if got.ScannedCount != 2000 || got.CurrentPath != "" {
		t.Errorf("after clear: progress = (%d, %q), want (2000, \"\")", got.ScannedCount, got.CurrentPath)
	}
}

func TestMarkInterruptedScans(t *testing.T) {
	r := setupScanProgressRepo(t)
	rows := []*ScanSession{
		{FilesystemID: 1, Status: "scanning", ScannedCount: 42, CurrentPath: "a/b"},
		{FilesystemID: 1, Status: "scanning"},
		{FilesystemID: 1, Status: "scanned"},
		{FilesystemID: 1, Status: "tagging"},
		{FilesystemID: 1, Status: "error", ErrorMessage: "boom"},
		{FilesystemID: 1, Status: "done"},
	}
	for _, s := range rows {
		if err := r.CreateSession(s); err != nil {
			t.Fatalf("create session %s: %v", s.Status, err)
		}
	}

	n, err := r.MarkInterruptedScans()
	if err != nil {
		t.Fatalf("MarkInterruptedScans: %v", err)
	}
	if n != 2 {
		t.Fatalf("flipped %d rows, want 2", n)
	}

	sessions, err := r.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	byStatus := map[string]int{}
	for _, s := range sessions {
		byStatus[s.Status]++
		switch s.ID {
		case rows[0].ID, rows[1].ID:
			if s.Status != "interrupted" {
				t.Errorf("session %d status = %s, want interrupted", s.ID, s.Status)
			}
			if s.ErrorMessage != ScanInterruptedMessage {
				t.Errorf("session %d error_message = %q, want %q", s.ID, s.ErrorMessage, ScanInterruptedMessage)
			}
			if s.CurrentPath != "" {
				t.Errorf("session %d current_path = %q, want cleared", s.ID, s.CurrentPath)
			}
			if s.ScannedCount != 42 && s.ID == rows[0].ID {
				t.Errorf("session %d scanned_count = %d, want preserved 42 (progress at interruption)", s.ID, s.ScannedCount)
			}
		}
	}
	if byStatus["scanning"] != 0 || byStatus["interrupted"] != 2 || byStatus["scanned"] != 1 || byStatus["error"] != 1 {
		t.Errorf("status distribution = %v", byStatus)
	}
}

func TestInitMarksZombieScansInterrupted(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(DB)
	zombie := &ScanSession{FilesystemID: 1, Status: "scanning", CurrentPath: "x/y"}
	if err := r.CreateSession(zombie); err != nil {
		t.Fatalf("create zombie: %v", err)
	}

	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	got, err := NewRepository(DB).GetSession(zombie.ID)
	if err != nil {
		t.Fatalf("get zombie: %v", err)
	}
	if got.Status != "interrupted" || got.ErrorMessage != ScanInterruptedMessage {
		t.Errorf("after restart: status=%s error_message=%q, want interrupted/%q",
			got.Status, got.ErrorMessage, ScanInterruptedMessage)
	}
}

func TestRefreshSessionCountsOnlyWritesCountColumns(t *testing.T) {
	r := setupScanProgressRepo(t)
	session := &ScanSession{FilesystemID: 1, Status: "scanning"}
	if err := r.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, f := range []FileEntry{
		{ScanSessionID: session.ID, OriginalPath: "a", Name: "a", FileType: "file", Size: 10, Tagged: true, Operation: "move"},
		{ScanSessionID: session.ID, OriginalPath: "b", Name: "b", FileType: "file", Size: 20},
	} {
		fe := f
		if err := r.CreateFile(&fe); err != nil {
			t.Fatalf("create file: %v", err)
		}
	}

	if err := r.db.Model(&ScanSession{}).Where("id = ?", session.ID).
		Updates(map[string]interface{}{
			"status":        "scanned",
			"scanned_count": 2,
			"current_path":  "",
			"error_message": "",
		}).Error; err != nil {
		t.Fatalf("terminal update: %v", err)
	}

	if err := r.RefreshSessionCounts(session.ID); err != nil {
		t.Fatalf("RefreshSessionCounts: %v", err)
	}

	got, err := r.GetSession(session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.TotalFiles != 2 || got.TaggedFiles != 1 || got.PlannedOps != 1 || got.TotalSize != 30 {
		t.Errorf("counts = total %d tagged %d planned %d size %d, want 2/1/1/30",
			got.TotalFiles, got.TaggedFiles, got.PlannedOps, got.TotalSize)
	}
	if got.Status != "scanned" {
		t.Errorf("status = %s, want scanned (lost update would have resurrected scanning)", got.Status)
	}
	if got.ScannedCount != 2 || got.CurrentPath != "" || got.ErrorMessage != "" {
		t.Errorf("progress fields = %d/%q/%q, want 2/empty/empty", got.ScannedCount, got.CurrentPath, got.ErrorMessage)
	}
}

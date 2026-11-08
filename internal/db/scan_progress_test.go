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

// UpdateScanProgress must write only the two progress columns — a full-row
// Save from the scanner's stale session copy would clobber concurrent status
// writes from the API layer.
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

	// The heartbeat also clears the path (walk done, saving results).
	if err := r.UpdateScanProgress(session.ID, 2000, ""); err != nil {
		t.Fatalf("UpdateScanProgress clear: %v", err)
	}
	got, _ = r.GetSession(session.ID)
	if got.ScannedCount != 2000 || got.CurrentPath != "" {
		t.Errorf("after clear: progress = (%d, %q), want (2000, \"\")", got.ScannedCount, got.CurrentPath)
	}
}

// Sessions stuck in "scanning" at process start are zombies from a previous
// lifetime: the sweep must flip exactly those to interrupted, leave every
// other status alone, and stamp an explanatory error message.
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

// The startup sweep is wired into Init itself: a database reopened with a
// leftover "scanning" row comes up with that row already interrupted.
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

	// Simulate a restart: same database file, fresh Init.
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

// RefreshSessionCounts runs on every session GET — exactly while a scan or
// agent run is writing the same row. It must touch ONLY the aggregate count
// columns: the old full-row Save was a lost update that could resurrect
// status="scanning" over a finished run's terminal state (and the startup
// sweep would then mislabel that completed scan as interrupted). This pins
// the contract: the row is already in its terminal state when the refresh
// lands, and every non-count column must survive.
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

	// The terminal write of the finished scan lands first (this is the state
	// the lost update used to clobber).
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
	// Count columns recomputed.
	if got.TotalFiles != 2 || got.TaggedFiles != 1 || got.PlannedOps != 1 || got.TotalSize != 30 {
		t.Errorf("counts = total %d tagged %d planned %d size %d, want 2/1/1/30",
			got.TotalFiles, got.TaggedFiles, got.PlannedOps, got.TotalSize)
	}
	// Everything else preserved — status above all.
	if got.Status != "scanned" {
		t.Errorf("status = %s, want scanned (lost update would have resurrected scanning)", got.Status)
	}
	if got.ScannedCount != 2 || got.CurrentPath != "" || got.ErrorMessage != "" {
		t.Errorf("progress fields = %d/%q/%q, want 2/empty/empty", got.ScannedCount, got.CurrentPath, got.ErrorMessage)
	}
}

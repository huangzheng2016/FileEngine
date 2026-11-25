package db

import (
	"path/filepath"
	"testing"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
)

func setupScanProgressRepo(t *testing.T) *Repository {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Open(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	return NewRepository(dal.DB())
}

func TestUpdateScanProgress(t *testing.T) {
	r := setupScanProgressRepo(t)
	session := &model.ScanSession{FilesystemID: 1, Status: "scanning", TotalFiles: 7}
	if err := r.DB().Create(session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := r.UpdateScanProgress(session.ID, ScanProgress{Scanned: 1234, CurrentPath: "photos/raw",
		Phase: ScanPhaseDuplicates, Done: 5 << 30, Total: 9 << 30}); err != nil {
		t.Fatalf("UpdateScanProgress: %v", err)
	}

	got, err := r.GetSession(session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.ScannedCount != 1234 || got.CurrentPath != "photos/raw" {
		t.Errorf("progress = (%d, %q), want (1234, photos/raw)", got.ScannedCount, got.CurrentPath)
	}
	if got.ScanPhase != ScanPhaseDuplicates || got.PhaseDone != 5<<30 || got.PhaseTotal != 9<<30 {
		t.Errorf("phase = %q %d/%d, want duplicates %d/%d", got.ScanPhase, got.PhaseDone, got.PhaseTotal, 5<<30, 9<<30)
	}
	if got.Status != "scanning" || got.TotalFiles != 7 {
		t.Errorf("unrelated columns clobbered: status=%s total_files=%d", got.Status, got.TotalFiles)
	}

	if err := r.UpdateScanProgress(session.ID, ScanProgress{Scanned: 2000}); err != nil {
		t.Fatalf("UpdateScanProgress clear: %v", err)
	}
	got, _ = r.GetSession(session.ID)
	if got.ScannedCount != 2000 || got.CurrentPath != "" || got.ScanPhase != "" || got.PhaseDone != 0 || got.PhaseTotal != 0 {
		t.Errorf("after clear: progress = (%d, %q, %q %d/%d), want (2000, \"\", none)",
			got.ScannedCount, got.CurrentPath, got.ScanPhase, got.PhaseDone, got.PhaseTotal)
	}
}

// Every write that starts, ends or abandons a scan clears its phase.
func TestScanLifecycleClearsPhase(t *testing.T) {
	r := setupScanProgressRepo(t)
	running := ScanProgress{Phase: ScanPhaseMetadata, Done: 3, Total: 10}
	for name, end := range map[string]func(id uint) error{
		"begin":  r.BeginScan,
		"finish": func(id uint) error { return r.FinishScan(id, 1, "") },
		"fail":   func(id uint) error { return r.FailScan(id, "boom") },
		"interrupt": func(uint) error {
			_, err := r.MarkInterruptedScans()
			return err
		},
	} {
		s := &model.ScanSession{FilesystemID: 1, Status: "scanning"}
		if err := r.DB().Create(s).Error; err != nil {
			t.Fatal(err)
		}
		if err := r.UpdateScanProgress(s.ID, running); err != nil {
			t.Fatal(err)
		}
		if err := end(s.ID); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := r.GetSession(s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ScanPhase != "" || got.PhaseDone != 0 || got.PhaseTotal != 0 {
			t.Errorf("%s: phase = %q %d/%d, want cleared", name, got.ScanPhase, got.PhaseDone, got.PhaseTotal)
		}
	}
}

func TestMarkInterruptedScans(t *testing.T) {
	r := setupScanProgressRepo(t)
	rows := []*model.ScanSession{
		{FilesystemID: 1, Status: "scanning", ScannedCount: 42, CurrentPath: "a/b"},
		{FilesystemID: 1, Status: "scanning"},
		{FilesystemID: 1, Status: "scanned"},
		{FilesystemID: 1, Status: "tagging"},
		{FilesystemID: 1, Status: "error", ErrorMessage: "boom"},
		{FilesystemID: 1, Status: "done"},
	}
	for _, s := range rows {
		if err := r.DB().Create(s).Error; err != nil {
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

	var sessions []model.ScanSession
	if err := r.DB().Find(&sessions).Error; err != nil {
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
	if err := Open(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(dal.DB())
	zombie := &model.ScanSession{FilesystemID: 1, Status: "scanning", CurrentPath: "x/y"}
	if err := r.DB().Create(zombie).Error; err != nil {
		t.Fatalf("create zombie: %v", err)
	}

	if err := Open(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	got, err := NewRepository(dal.DB()).GetSession(zombie.ID)
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
	session := &model.ScanSession{FilesystemID: 1, Status: "scanning"}
	if err := r.DB().Create(session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, f := range []model.FileEntry{
		{ScanSessionID: session.ID, OriginalPath: "a", Name: "a", FileType: "file", Size: 10, Tagged: true, Operation: "move"},
		{ScanSessionID: session.ID, OriginalPath: "b", Name: "b", FileType: "file", Size: 20},
	} {
		fe := f
		if err := r.DB().Create(&fe).Error; err != nil {
			t.Fatalf("create file: %v", err)
		}
	}

	if err := r.db.Model(&model.ScanSession{}).Where("id = ?", session.ID).
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

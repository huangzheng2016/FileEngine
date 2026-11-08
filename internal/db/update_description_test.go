package db

import (
	"path/filepath"
	"testing"

	"FileEngine/internal/config"
)

// UpdateDescription must be column-scoped: when a parallel tool call (eino
// executes tool calls concurrently) has just moved new_path forward, writing
// a description must not restore the stale row. The old implementation saved
// the whole previously-loaded struct and silently clobbered the racing
// set_target — new_path was lost while the tool still reported success.
func TestUpdateDescriptionColumnScoped(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(DB)

	fe := &FileEntry{ScanSessionID: 1, OriginalPath: "cam/dir", Name: "dir", FileType: "directory"}
	if err := r.CreateFile(fe); err != nil {
		t.Fatalf("create file: %v", err)
	}

	// A racing set_target commits first.
	if _, _, err := r.SetTargetUnique(1, "cam/dir", "/surveillance-recordings/dir", "directory"); err != nil {
		t.Fatalf("set target: %v", err)
	}
	// The description write must only touch its own column.
	if err := r.UpdateDescription(1, "cam/dir", "监控录像存档"); err != nil {
		t.Fatalf("update description: %v", err)
	}

	got, err := r.GetFileByPath(1, "cam/dir")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Description != "监控录像存档" {
		t.Errorf("description = %q, want written", got.Description)
	}
	if got.NewPath != "/surveillance-recordings/dir" || got.Operation != "planned" {
		t.Errorf("clobbered: new_path=%q operation=%q", got.NewPath, got.Operation)
	}
}

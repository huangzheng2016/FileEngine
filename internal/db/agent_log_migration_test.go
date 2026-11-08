package db

import (
	"path/filepath"
	"testing"

	"FileEngine/internal/config"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Rows written before the worker-lane schema (agent_logs without worker_id)
// must survive AutoMigrate and still scan — the frontend renders them as the
// single worker-0 lane. A bare ADD COLUMN would leave NULLs that fail int
// scans; the model's default:0 tag backfills existing rows on all dialects.
func TestAgentLogWorkerIDBackfill(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "old.db")
	raw, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// The pre-lanes agent_logs schema (no worker_id column) with one old row.
	if err := raw.Exec(`CREATE TABLE agent_logs (
		id integer primary key,
		scan_session_id integer,
		batch_index integer,
		role text, tool_name text, tool_input text, tool_output text, content text,
		prompt_tokens integer, completion_tokens integer, total_tokens integer,
		created_at datetime)`).Error; err != nil {
		t.Fatalf("create old table: %v", err)
	}
	if err := raw.Exec(`INSERT INTO agent_logs
		(scan_session_id, batch_index, role, content, created_at)
		VALUES (1, 3, 'assistant', 'old row', datetime('now'))`).Error; err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	sqlDB, _ := raw.DB()
	_ = sqlDB.Close()

	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init+migrate: %v", err)
	}
	r := NewRepository(DB)
	logs, total, err := r.ListAgentLogs(LogQuery{SessionID: 1})
	if err != nil {
		t.Fatalf("old rows must still scan after migrate: %v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("expected the one old row, got total=%d logs=%d", total, len(logs))
	}
	if logs[0].WorkerID != 0 {
		t.Fatalf("pre-lane rows must backfill to worker 0, got %d", logs[0].WorkerID)
	}
}

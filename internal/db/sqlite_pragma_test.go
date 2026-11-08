package db

import (
	"path/filepath"
	"testing"

	"FileEngine/internal/config"
)

// TestSQLitePragmas pins the per-connection settings the pure-Go driver must
// apply from the DSN: WAL journal, 30s busy timeout, NORMAL sync, foreign keys.
func TestSQLitePragmas(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "pragma.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	cases := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"busy_timeout", "30000"},
		{"synchronous", "1"},
		{"foreign_keys", "1"},
		{"cache_size", "-20000"},
	}
	for _, c := range cases {
		var got string
		if err := sqlDB.QueryRow("PRAGMA " + c.pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", c.pragma, err)
		}
		if got != c.want {
			t.Errorf("PRAGMA %s = %q, want %q", c.pragma, got, c.want)
		}
	}
}

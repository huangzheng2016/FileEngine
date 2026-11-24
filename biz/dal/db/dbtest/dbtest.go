// Package dbtest gives tests a fresh migrated SQLite database. It is imported
// only from _test.go files.
package dbtest

import (
	"path/filepath"
	"testing"

	"FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/query"
	"FileEngine/internal/config"
)

// New returns a typed query layer over an empty database in the test's
// temporary directory; it is closed when the test ends.
func New(t *testing.T) *query.Query {
	t.Helper()
	g, err := db.Open(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, err := g.DB(); err == nil {
			_ = s.Close()
		}
	})
	return query.Use(g)
}

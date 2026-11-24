package db

import (
	"strings"
	"testing"

	"FileEngine/biz/dal/db/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestApplyFileFiltersPostgresSQL(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                 "host=localhost dbname=x sslmode=disable",
		WithoutQuotingCheck: true,
	}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open postgres dialector: %v", err)
	}
	tx := applyFileFilters(db.Session(&gorm.Session{DryRun: true}).Model(&model.FileEntry{}),
		FileQuery{SessionID: 7, Search: `100%_x`})
	var files []model.FileEntry
	stmt := tx.Find(&files).Statement

	sql := stmt.SQL.String()
	if !strings.Contains(sql, "ILIKE") {
		t.Fatalf("postgres search SQL must use ILIKE: %s", sql)
	}
	if !strings.Contains(sql, "ESCAPE '!'") {
		t.Fatalf("postgres search SQL must carry the escape clause: %s", sql)
	}
	var foundPattern bool
	for _, v := range stmt.Vars {
		if s, ok := v.(string); ok && s == "%100!%!_x%" {
			foundPattern = true
		}
	}
	if !foundPattern {
		t.Fatalf("escaped search pattern missing from bind vars: %v", stmt.Vars)
	}
}

package db

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestLikeLiteral(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain/path", "plain/path"},
		{"my_dir", "my!_dir"},
		{"100%", "100!%"},
		{"a!b", "a!!b"},
		{"!_%", "!!!_!%"},
		{"照片/2024", "照片/2024"},
	}
	for _, tc := range cases {
		if got := likeLiteral(tc.in); got != tc.want {
			t.Errorf("likeLiteral(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNameSearchConditionPerDialect(t *testing.T) {
	cases := []struct {
		name      string
		dialector gorm.Dialector
		wantOp    string
	}{
		{"sqlite", sqlite.Open("file::memory:?cache=shared"), "name LIKE ?"},
		{"postgres", postgres.New(postgres.Config{DSN: "host=localhost dbname=x sslmode=disable", WithoutQuotingCheck: true}), "name ILIKE ?"},
		{"mysql", mysql.New(mysql.Config{DSN: "u:p@tcp(127.0.0.1:3306)/x", SkipInitializeWithVersion: true}), "name COLLATE utf8mb4_general_ci LIKE ?"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(tc.dialector, &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatalf("open %s dialector: %v", tc.name, err)
			}
			got := nameSearchCondition(db)
			want := tc.wantOp + likeEscapeClause
			if got != want {
				t.Fatalf("nameSearchCondition(%s): got %q, want %q", tc.name, got, want)
			}
		})
	}
}

func TestApplyFileFiltersPostgresSQL(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                 "host=localhost dbname=x sslmode=disable",
		WithoutQuotingCheck: true,
	}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open postgres dialector: %v", err)
	}
	tx := applyFileFilters(db.Session(&gorm.Session{DryRun: true}).Model(&FileEntry{}),
		FileQuery{SessionID: 7, Search: `100%_x`})
	var files []FileEntry
	stmt := tx.Find(&files).Statement

	sql := stmt.SQL.String()
	if !contains(sql, "ILIKE") {
		t.Fatalf("postgres search SQL must use ILIKE: %s", sql)
	}
	if !contains(sql, "ESCAPE '!'") {
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

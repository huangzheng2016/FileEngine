package db

import (
	"path/filepath"
	"testing"

	"FileEngine/internal/config"
)

func TestSQLiteReadPool(t *testing.T) {
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "read.db")}); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() {
		if s, err := DB.DB(); err == nil {
			s.Close()
		}
		if s, err := readDB.DB(); err == nil {
			s.Close()
		}
	})
	if ReadDB() == DB {
		t.Fatal("ReadDB is the writer pool on SQLite")
	}
	repo := NewRepository(DB)
	if repo.Reader() != ReadDB() {
		t.Error("Repository.Reader does not use the read pool for the global DB")
	}
	if err := repo.CreateSession(&ScanSession{RootPath: "x"}); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := ReadDB().Model(&ScanSession{}).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("read pool sees %d sessions (err %v), want the committed row", n, err)
	}
	if err := ReadDB().Create(&ScanSession{RootPath: "y"}).Error; err == nil {
		t.Error("read pool accepted a write")
	}
	var mode string
	ReadDB().Raw("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

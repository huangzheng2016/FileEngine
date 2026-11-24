package db

import (
	"path/filepath"
	"testing"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
)

func TestSQLiteReadPool(t *testing.T) {
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "read.db")}); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() {
		if s, err := DB().DB(); err == nil {
			s.Close()
		}
		if s, err := readDB.DB(); err == nil {
			s.Close()
		}
	})
	if ReadDB() == DB() {
		t.Fatal("ReadDB is the writer pool on SQLite")
	}
	if ReadQuery() == Query() {
		t.Fatal("ReadQuery is the writer query on SQLite")
	}
	if err := DB().Create(&model.ScanSession{RootPath: "x"}).Error; err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := ReadDB().Model(&model.ScanSession{}).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("read pool sees %d sessions (err %v), want the committed row", n, err)
	}
	if err := ReadDB().Create(&model.ScanSession{RootPath: "y"}).Error; err == nil {
		t.Error("read pool accepted a write")
	}
	var mode string
	ReadDB().Raw("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

// Package admin holds operator actions that are not tied to one resource.
package admin

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"FileEngine/biz/errs"

	"gorm.io/gorm"
)

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

// Backup is a consistent snapshot ready to download. Closing it deletes the
// temporary file.
type Backup struct {
	io.ReadCloser
	Name string
	Size int64
}

type tempFile struct {
	*os.File
	dir string
}

func (t *tempFile) Close() error {
	err := t.File.Close()
	_ = os.RemoveAll(t.dir)
	return err
}

// Backup writes a snapshot with VACUUM INTO, which stays consistent while the
// app keeps writing. Other databases have their own tools.
func (s *Service) Backup(ctx context.Context) (*Backup, error) {
	if s.db.Dialector.Name() != "sqlite" {
		return nil, errs.Invalid("仅 SQLite 数据库支持在线备份，MySQL/PostgreSQL 请使用数据库自带工具")
	}
	dir, err := os.MkdirTemp("", "fileengine-backup-*")
	if err != nil {
		return nil, err
	}
	snap := filepath.Join(dir, "snapshot.db")
	if err := s.db.WithContext(ctx).Exec("VACUUM INTO ?", snap).Error; err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("vacuum into: %w", err)
	}
	f, err := os.Open(snap)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &Backup{
		ReadCloser: &tempFile{File: f, dir: dir},
		Name:       "fileengine-" + time.Now().Format("20060102-150405") + ".db",
		Size:       st.Size(),
	}, nil
}

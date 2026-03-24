package remotefs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type LocalFS struct {
	basePath string
}

func NewLocalFS(basePath string) (*LocalFS, error) {
	abs, err := filepath.Abs(basePath)
	if err != nil {
		return nil, fmt.Errorf("invalid base path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("base path not accessible: %w", err)
	}
	return &LocalFS{basePath: abs}, nil
}

func (l *LocalFS) resolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(l.basePath, path)
}

func (l *LocalFS) List(_ context.Context, path string) ([]FileInfo, error) {
	fullPath := l.resolve(path)
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}

	var result []FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		ft := FileTypeFile
		if entry.IsDir() {
			ft = FileTypeDirectory
		} else if entry.Type()&os.ModeSymlink != 0 {
			ft = FileTypeSymlink
		}
		result = append(result, FileInfo{
			Name:        entry.Name(),
			Path:        filepath.Join(path, entry.Name()),
			Size:        info.Size(),
			ModTime:     info.ModTime(),
			Permissions: fmt.Sprintf("%04o", info.Mode().Perm()),
			FileType:    ft,
			IsDir:       entry.IsDir(),
		})
	}
	return result, nil
}

func (l *LocalFS) Stat(_ context.Context, path string) (*FileInfo, error) {
	fullPath := l.resolve(path)
	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}
	ft := FileTypeFile
	if info.IsDir() {
		ft = FileTypeDirectory
	} else if info.Mode()&os.ModeSymlink != 0 {
		ft = FileTypeSymlink
	}
	return &FileInfo{
		Name:        info.Name(),
		Path:        path,
		Size:        info.Size(),
		ModTime:     info.ModTime(),
		Permissions: fmt.Sprintf("%04o", info.Mode().Perm()),
		FileType:    ft,
		IsDir:       info.IsDir(),
	}, nil
}

func (l *LocalFS) ReadFile(_ context.Context, path string) (io.ReadCloser, error) {
	fullPath := l.resolve(path)
	return os.Open(fullPath)
}

func (l *LocalFS) MoveFile(_ context.Context, src, dst string) error {
	srcPath := l.resolve(src)
	dstPath := l.resolve(dst)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}
	return os.Rename(srcPath, dstPath)
}

func (l *LocalFS) CopyFile(_ context.Context, src, dst string) error {
	srcPath := l.resolve(src)
	dstPath := l.resolve(dst)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (l *LocalFS) MkdirAll(_ context.Context, path string) error {
	return os.MkdirAll(l.resolve(path), 0755)
}

func (l *LocalFS) Close() error {
	return nil
}

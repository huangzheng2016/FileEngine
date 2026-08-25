package remotefs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// resolve maps a caller path to a host path under basePath. Absolute paths are
// accepted only when they stay inside basePath; anything escaping basePath
// (absolute or via "..") is rejected, matching the root semantics of the
// SFTP/SMB implementations.
func (l *LocalFS) resolve(path string) (string, error) {
	full := path
	if filepath.IsAbs(full) {
		full = filepath.Clean(full)
	} else {
		full = filepath.Join(l.basePath, full)
	}
	rel, err := filepath.Rel(l.basePath, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes base path %q", path, l.basePath)
	}
	return full, nil
}

func (l *LocalFS) List(_ context.Context, path string) ([]FileInfo, error) {
	fullPath, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
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
	fullPath, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
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
	fullPath, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.Open(fullPath)
}

func (l *LocalFS) MoveFile(_ context.Context, src, dst string) error {
	srcPath, err := l.resolve(src)
	if err != nil {
		return err
	}
	dstPath, err := l.resolve(dst)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}
	return os.Rename(srcPath, dstPath)
}

func (l *LocalFS) CopyFile(_ context.Context, src, dst string) error {
	srcPath, err := l.resolve(src)
	if err != nil {
		return err
	}
	dstPath, err := l.resolve(dst)
	if err != nil {
		return err
	}
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

func (l *LocalFS) Delete(_ context.Context, p string) error {
	fullPath, err := l.resolve(p)
	if err != nil {
		return err
	}
	return os.RemoveAll(fullPath)
}

func (l *LocalFS) MkdirAll(_ context.Context, path string) error {
	fullPath, err := l.resolve(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(fullPath, 0755)
}

func (l *LocalFS) Close() error {
	return nil
}

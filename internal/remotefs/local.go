package remotefs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalFS needs no timeout machinery: its operations are plain syscalls —
// there is no dial and no remote peer that can silently stop responding.
// Errors are still wrapped in OpError for a uniform shape across backends.
type LocalFS struct {
	basePath     string
	resolvedBase string // basePath with symlinks resolved (macOS /var etc.)
	// denied are resolved host paths (app data dir, DB, config, key file)
	// that stay unreachable even when they lie inside basePath: resolve
	// rejects them and List hides them. Set by NewFromConfig from the
	// LocalPolicy; nil for a bare NewLocalFS.
	denied []string
}

func NewLocalFS(basePath string) (*LocalFS, error) {
	abs, err := filepath.Abs(basePath)
	if err != nil {
		return nil, fmt.Errorf("invalid base path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("base path not accessible: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve base path: %w", err)
	}
	return &LocalFS{basePath: abs, resolvedBase: resolved}, nil
}

// withinBase reports whether path is lexically contained in base.
func withinBase(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExisting resolves symlinks for path; when path does not exist (write
// destinations), it resolves the nearest existing ancestor and re-appends the
// remainder.
func resolveExisting(path string) (string, error) {
	var suffix []string
	p := path
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		suffix = append(suffix, filepath.Base(p))
		p = parent
	}
}

// resolve maps a caller path to a host path under basePath. Absolute paths are
// accepted only when they stay inside basePath; anything escaping basePath
// (absolute, via "..", or via a symlink pointing outside) is rejected,
// matching the root semantics of the SFTP/SMB implementations.
func (l *LocalFS) resolve(path string) (string, error) {
	full := path
	if filepath.IsAbs(full) {
		full = filepath.Clean(full)
	} else {
		full = filepath.Join(l.basePath, full)
	}
	if !withinBase(full, l.basePath) {
		return "", fmt.Errorf("path %q escapes base path %q", path, l.basePath)
	}
	// Lexical containment is not enough: a symlink inside the base may point
	// outside. Resolve the nearest existing ancestor (the target itself may
	// not exist yet for write destinations) and re-check containment.
	resolved, err := resolveExisting(full)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	if !withinBase(resolved, l.resolvedBase) {
		return "", fmt.Errorf("path %q escapes base path %q via symlink", path, l.basePath)
	}
	if l.isDenied(resolved) {
		return "", fmt.Errorf("path %q is inside FileEngine's own data directory", path)
	}
	return full, nil
}

func (l *LocalFS) isDenied(resolved string) bool {
	for _, d := range l.denied {
		if withinBase(resolved, d) {
			return true
		}
	}
	return false
}

func (l *LocalFS) List(_ context.Context, path string) ([]FileInfo, error) {
	fullPath, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, &OpError{Protocol: "local", Op: "list", Path: path, Err: err}
	}

	// Hide denied entries from listings (access is refused by resolve in any
	// case); only directories that contain a denied path pay for the check.
	hide := false
	if dir, err := resolveExisting(fullPath); err == nil {
		for _, d := range l.denied {
			if withinBase(d, dir) {
				hide = true
			}
		}
	}

	var result []FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if hide {
			if r, err := resolveExisting(filepath.Join(fullPath, entry.Name())); err != nil || l.isDenied(r) {
				continue
			}
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
		return nil, &OpError{Protocol: "local", Op: "stat", Path: path, Err: err}
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
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, &OpError{Protocol: "local", Op: "open", Path: path, Err: err}
	}
	return f, nil
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

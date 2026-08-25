package remotefs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupLocalFS(t *testing.T) (*LocalFS, string) {
	t.Helper()
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "sub", "f.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	fs, err := NewLocalFS(base)
	if err != nil {
		t.Fatalf("NewLocalFS: %v", err)
	}
	return fs, base
}

func wantEscapeErr(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected escape error, got nil", what)
	}
	if !strings.Contains(err.Error(), "escapes base path") {
		t.Fatalf("%s: got %v, want 'escapes base path' error", what, err)
	}
}

// Absolute paths used to bypass BasePath entirely (finding: scan_path="/"
// scanned the whole machine). resolve must now reject anything escaping
// basePath while still accepting absolute paths inside it.
func TestLocalFSResolveEscapes(t *testing.T) {
	fs, base := setupLocalFS(t)
	defer fs.Close()
	ctx := context.Background()

	// Relative paths inside base still work.
	if _, err := fs.List(ctx, "sub"); err != nil {
		t.Fatalf("List relative inside: %v", err)
	}
	// Absolute paths inside base still work.
	if _, err := fs.List(ctx, filepath.Join(base, "sub")); err != nil {
		t.Fatalf("List absolute inside: %v", err)
	}
	rc, err := fs.ReadFile(ctx, filepath.Join(base, "sub", "f.txt"))
	if err != nil {
		t.Fatalf("ReadFile absolute inside: %v", err)
	}
	rc.Close()

	// Absolute path outside base is rejected.
	if _, err := fs.List(ctx, "/"); err == nil {
		t.Fatal("List /: expected escape error, got nil")
	}
	wantEscapeErr(t, "List absolute outside", func() error {
		_, err := fs.List(ctx, filepath.Dir(base))
		return err
	}())
	wantEscapeErr(t, "ReadFile absolute outside", func() error {
		_, err := fs.ReadFile(ctx, "/etc/hosts")
		return err
	}())

	// Relative ".." escape is rejected too.
	wantEscapeErr(t, "List dotdot escape", func() error {
		_, err := fs.List(ctx, "../../..")
		return err
	}())
	wantEscapeErr(t, "Stat dotdot escape", func() error {
		_, err := fs.Stat(ctx, "..")
		return err
	}())

	// Write-side operations are covered as well.
	wantEscapeErr(t, "MkdirAll escape", fs.MkdirAll(ctx, "../evil"))
	wantEscapeErr(t, "MoveFile dst escape", fs.MoveFile(ctx, "sub/f.txt", "../escape.txt"))
	wantEscapeErr(t, "CopyFile dst escape", fs.CopyFile(ctx, "sub/f.txt", "../escape.txt"))
	wantEscapeErr(t, "Delete escape", fs.Delete(ctx, "../"))

	// The rejected moves must not have created anything outside base.
	if _, err := os.Stat(filepath.Join(filepath.Dir(base), "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("escape.txt exists outside base: %v", err)
	}

	// Same-FS move inside base still works after the fix.
	if err := fs.MoveFile(ctx, "sub/f.txt", "sub/g.txt"); err != nil {
		t.Fatalf("MoveFile inside base: %v", err)
	}
	if _, err := fs.Stat(ctx, "sub/g.txt"); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
}

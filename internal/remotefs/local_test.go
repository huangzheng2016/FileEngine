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

func TestLocalFSResolveEscapes(t *testing.T) {
	fs, base := setupLocalFS(t)
	defer fs.Close()
	ctx := context.Background()

	if _, err := fs.List(ctx, "sub"); err != nil {
		t.Fatalf("List relative inside: %v", err)
	}
	if _, err := fs.List(ctx, filepath.Join(base, "sub")); err != nil {
		t.Fatalf("List absolute inside: %v", err)
	}
	rc, err := fs.ReadFile(ctx, filepath.Join(base, "sub", "f.txt"))
	if err != nil {
		t.Fatalf("ReadFile absolute inside: %v", err)
	}
	rc.Close()

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

	wantEscapeErr(t, "List dotdot escape", func() error {
		_, err := fs.List(ctx, "../../..")
		return err
	}())
	wantEscapeErr(t, "Stat dotdot escape", func() error {
		_, err := fs.Stat(ctx, "..")
		return err
	}())

	wantEscapeErr(t, "MkdirAll escape", fs.MkdirAll(ctx, "../evil"))
	wantEscapeErr(t, "MoveFile dst escape", fs.MoveFile(ctx, "sub/f.txt", "../escape.txt"))
	wantEscapeErr(t, "CopyFile dst escape", fs.CopyFile(ctx, "sub/f.txt", "../escape.txt"))
	wantEscapeErr(t, "Delete escape", fs.Delete(ctx, "../"))

	if _, err := os.Stat(filepath.Join(filepath.Dir(base), "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("escape.txt exists outside base: %v", err)
	}

	if err := fs.MoveFile(ctx, "sub/f.txt", "sub/g.txt"); err != nil {
		t.Fatalf("MoveFile inside base: %v", err)
	}
	if _, err := fs.Stat(ctx, "sub/g.txt"); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
}

func TestLocalFSSymlinkEscapes(t *testing.T) {
	fs, base := setupLocalFS(t)
	defer fs.Close()
	ctx := context.Background()

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("shh"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "evil")); err != nil {
		t.Fatalf("symlink evil: %v", err)
	}
	if err := os.Symlink(filepath.Join(base, "sub"), filepath.Join(base, "good")); err != nil {
		t.Fatalf("symlink good: %v", err)
	}

	if _, err := fs.List(ctx, "good"); err != nil {
		t.Fatalf("List inside symlink: %v", err)
	}
	rc, err := fs.ReadFile(ctx, "good/f.txt")
	if err != nil {
		t.Fatalf("ReadFile inside symlink: %v", err)
	}
	rc.Close()

	wantEscapeErr(t, "List symlink escape", func() error {
		_, err := fs.List(ctx, "evil")
		return err
	}())
	wantEscapeErr(t, "ReadFile symlink escape", func() error {
		_, err := fs.ReadFile(ctx, "evil/secret.txt")
		return err
	}())
	wantEscapeErr(t, "Stat symlink escape", func() error {
		_, err := fs.Stat(ctx, "evil")
		return err
	}())

	wantEscapeErr(t, "MkdirAll symlink escape", fs.MkdirAll(ctx, "evil/newdir"))
	wantEscapeErr(t, "MoveFile symlink escape", fs.MoveFile(ctx, "sub/f.txt", "evil/moved.txt"))
	wantEscapeErr(t, "CopyFile symlink escape", fs.CopyFile(ctx, "sub/f.txt", "evil/copy.txt"))
	wantEscapeErr(t, "Delete symlink escape", fs.Delete(ctx, "evil/secret.txt"))

	for _, name := range []string{"moved.txt", "copy.txt", "newdir"} {
		if _, err := os.Stat(filepath.Join(outside, name)); !os.IsNotExist(err) {
			t.Fatalf("%s exists outside base: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatalf("outside secret.txt must survive Delete attempt: %v", err)
	}
	if _, err := fs.Stat(ctx, "sub/f.txt"); err != nil {
		t.Fatalf("source file must survive rejected move: %v", err)
	}
}

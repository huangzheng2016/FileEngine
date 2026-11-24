package remotefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFileAnnouncesCreateOnlyOnSuccess(t *testing.T) {
	root := t.TempDir()
	fs, err := NewLocalFS(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "taken.txt"), []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ctx := WithCreateNotice(context.Background(), func() error { calls++; return nil })
	if err := fs.CopyFile(ctx, "a.txt", "taken.txt"); !errors.Is(err, os.ErrExist) || calls != 0 {
		t.Fatalf("existing dst: err=%v calls=%d", err, calls)
	}
	if err := fs.CopyFile(ctx, "a.txt", "b.txt"); err != nil || calls != 1 {
		t.Fatalf("new dst: err=%v calls=%d", err, calls)
	}
}

func TestCopyFileRemovesDstWhenNoticeFails(t *testing.T) {
	root := t.TempDir()
	fs, err := NewLocalFS(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := WithCreateNotice(context.Background(), func() error { return errors.New("journal down") })
	if err := fs.CopyFile(ctx, "a.txt", "b.txt"); err == nil {
		t.Fatal("copy must fail when the journal write fails")
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err == nil {
		t.Fatal("dst left behind")
	}
}

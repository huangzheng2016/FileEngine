package remotefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalWriteContract(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	write := func(p, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		b, _ := os.ReadFile(filepath.Join(dir, p))
		return string(b)
	}
	write("a.txt", "AAA")
	write("b.txt", "BBB")
	write("d/x.txt", "X")

	if err := fs.CopyFile(ctx, "a.txt", "b.txt"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("copy onto existing: want ErrExist, got %v", err)
	}
	if read("b.txt") != "BBB" {
		t.Fatal("existing dst was modified")
	}
	if err := fs.MoveFile(ctx, "a.txt", "b.txt"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("move onto existing: want ErrExist, got %v", err)
	}
	if err := fs.CopyFile(ctx, "d", "d2"); !errors.Is(err, ErrIsDir) {
		t.Fatalf("copy dir: want ErrIsDir, got %v", err)
	}
	if err := fs.CopyFile(ctx, "a.txt", "c.txt"); err != nil || read("c.txt") != "AAA" {
		t.Fatalf("copy: %v %q", err, read("c.txt"))
	}
	if err := fs.Delete(ctx, "d"); err == nil {
		t.Fatal("Delete of a non-empty directory must fail")
	}
	if read("d/x.txt") != "X" {
		t.Fatal("non-empty directory content removed")
	}
	if err := fs.MkdirAll(ctx, "e/f/g"); err != nil {
		t.Fatal(err)
	}
	if err := fs.MkdirAll(ctx, "e/f/g"); err != nil {
		t.Fatalf("MkdirAll must be idempotent: %v", err)
	}
	if err := fs.Delete(ctx, "e/f/g"); err != nil {
		t.Fatalf("delete empty dir: %v", err)
	}
}

func TestSlidingCtx(t *testing.T) {
	ctx, touch, cancel := slidingCtx(context.Background(), 80*time.Millisecond)
	defer cancel()
	for i := 0; i < 5; i++ {
		time.Sleep(40 * time.Millisecond)
		touch()
	}
	if ctx.Err() != nil {
		t.Fatal("progressing operation must not expire")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("idle operation must expire")
	}
}

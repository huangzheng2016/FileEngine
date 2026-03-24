package remotefs

import (
	"context"
	"testing"

	"FileEngine/internal/config"
)

const (
	testHost     = "hz2016-qnasmini"
	testUsername = "test"
	testPassword = "test123"
	testBasePath = "test"
)

// testListRoot verifies that the FS can connect, list root, and has entries.
func testListRoot(t *testing.T, fs RemoteFS) {
	t.Helper()
	ctx := context.Background()
	defer fs.Close()

	files, err := fs.List(ctx, ".")
	if err != nil {
		t.Fatalf("List root: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected entries in root, got 0")
	}
	t.Logf("Root has %d entries:", len(files))
	for _, f := range files {
		t.Logf("  %s (type=%s, size=%d)", f.Name, f.FileType, f.Size)
	}
}

func TestSMB_ListRoot(t *testing.T) {
	fs, err := NewSMBFS(config.RemoteFSConfig{
		Host: testHost, Username: testUsername, Password: testPassword, BasePath: testBasePath,
	})
	if err != nil {
		t.Fatalf("NewSMBFS: %v", err)
	}
	testListRoot(t, fs)
}

func TestFTP_ListRoot(t *testing.T) {
	fs, err := NewFTPFS(config.RemoteFSConfig{
		Host: testHost, Username: testUsername, Password: testPassword, BasePath: "/" + testBasePath,
	})
	if err != nil {
		t.Fatalf("NewFTPFS: %v", err)
	}
	testListRoot(t, fs)
}

func TestSFTP_ListRoot(t *testing.T) {
	fs, err := NewSFTPFS(config.RemoteFSConfig{
		Host: testHost, Username: testUsername, Password: testPassword, BasePath: "/vol1/1001/test",
	})
	if err != nil {
		t.Fatalf("NewSFTPFS: %v", err)
	}
	testListRoot(t, fs)
}

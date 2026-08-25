package remotefs

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"FileEngine/internal/config"
)

const (
	testHost     = "test-nas.invalid"
	testUsername = "test"
	testPassword = "not-a-real-password"
	testBasePath = "test"
)

// requireTestNAS skips the test when the hardcoded owner-LAN test NAS is not
// resolvable — these are integration tests that only run on that network.
func requireTestNAS(t *testing.T) {
	t.Helper()
	if _, err := net.LookupHost(testHost); err != nil {
		t.Skipf("test NAS %q not resolvable on this network: %v", testHost, err)
	}
}

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
	requireTestNAS(t)
	fs, err := NewSMBFS(config.RemoteFSConfig{
		Host: testHost, Username: testUsername, Password: testPassword, BasePath: testBasePath,
	})
	if err != nil {
		t.Fatalf("NewSMBFS: %v", err)
	}
	testListRoot(t, fs)
}

func TestFTP_ListRoot(t *testing.T) {
	requireTestNAS(t)
	fs, err := NewFTPFS(config.RemoteFSConfig{
		Host: testHost, Username: testUsername, Password: testPassword, BasePath: "/" + testBasePath,
	})
	if err != nil {
		t.Fatalf("NewFTPFS: %v", err)
	}
	testListRoot(t, fs)
}

func TestSFTP_ListRoot(t *testing.T) {
	requireTestNAS(t)
	fs, err := NewSFTPFS(config.RemoteFSConfig{
		Host: testHost, Username: testUsername, Password: testPassword, BasePath: "/vol1/1001/test",
	})
	if err != nil {
		t.Fatalf("NewSFTPFS: %v", err)
	}
	testListRoot(t, fs)
}

// testNFSHost exports "/fs/1000/nfs" as a whole (fnOS style), which exercises
// the deep-export MNT probe in NewNFSFS. The address comes from
// FILEENGINE_TEST_NFS_HOST so no LAN address lives in the repo; it falls back
// to the owner-LAN test NAS hostname and skips when unreachable.
func testNFSHost(t *testing.T) string {
	t.Helper()
	if h := os.Getenv("FILEENGINE_TEST_NFS_HOST"); h != "" {
		return h
	}
	return testHost
}

// dialTestNFS skips the test when the NFS server is unreachable.
func dialTestNFS(t *testing.T) {
	t.Helper()
	host := testNFSHost(t)
	conn, err := net.DialTimeout("tcp", host+":2049", 2*time.Second)
	if err != nil {
		t.Skipf("test NFS server %q not reachable on this network: %v", host, err)
	}
	conn.Close()
}

// TestNFS_ConcurrentReads hammers one NFSFS with parallel Open+Read calls —
// the access pattern of eino's parallel tool execution. The underlying
// rpc.Client is a single non-multiplexed stream; without serialization this
// fails with "xid did not match".
func TestNFS_ConcurrentReads(t *testing.T) {
	dialTestNFS(t)
	fs, err := NewNFSFS(config.RemoteFSConfig{Host: testNFSHost(t), BasePath: "/fs/1000/nfs"})
	if err != nil {
		t.Fatalf("NewNFSFS: %v", err)
	}
	defer fs.Close()
	ctx := context.Background()

	// Collect a handful of real files to read in parallel.
	entries, err := fs.List(ctx, "ShareSpace/Project")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir {
			paths = append(paths, "ShareSpace/Project/"+e.Name)
		}
	}
	if len(paths) < 4 {
		t.Skipf("need at least 4 files for a meaningful parallel read, got %d", len(paths))
	}

	const workers = 8
	errs := make(chan error, workers*len(paths))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		for _, p := range paths {
			wg.Add(1)
			go func(path string) {
				defer wg.Done()
				f, err := fs.ReadFile(ctx, path)
				if err != nil {
					errs <- fmt.Errorf("open %s: %w", path, err)
					return
				}
				defer f.Close()
				buf := make([]byte, 4096)
				if _, err := io.ReadAtLeast(f, buf, 256); err != nil {
					errs <- fmt.Errorf("read %s: %w", path, err)
				}
			}(p)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestNFS_DeepExport verifies mounting an export whose path is deeper than
// one segment: NewNFSFS must discover "/fs/1000/nfs" itself is the export.
func TestNFS_DeepExport(t *testing.T) {
	dialTestNFS(t)
	fs, err := NewNFSFS(config.RemoteFSConfig{Host: testNFSHost(t), BasePath: "/fs/1000/nfs"})
	if err != nil {
		t.Fatalf("NewNFSFS: %v", err)
	}
	testListRoot(t, fs)
}

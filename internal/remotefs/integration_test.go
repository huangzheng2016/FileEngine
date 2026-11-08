package remotefs

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"FileEngine/internal/config"
)

// Manual integration test: parallel listings over one go-smb2 session, which
// must multiplex concurrent requests; fails or trips -race if that breaks.
//
//	FILEENGINE_TEST_SMB_HOST=nas FILEENGINE_TEST_SMB_USER=u \
//	FILEENGINE_TEST_SMB_PASSWORD=p FILEENGINE_TEST_SMB_SHARE=share \
//	go test ./internal/remotefs -run TestSMB_ConcurrentListByHandle -race -v
func TestSMB_ConcurrentListByHandle(t *testing.T) {
	host := os.Getenv("FILEENGINE_TEST_SMB_HOST")
	user := os.Getenv("FILEENGINE_TEST_SMB_USER")
	pass := os.Getenv("FILEENGINE_TEST_SMB_PASSWORD")
	share := os.Getenv("FILEENGINE_TEST_SMB_SHARE")
	if host == "" || user == "" || pass == "" || share == "" {
		t.Skip("set FILEENGINE_TEST_SMB_HOST, _USER, _PASSWORD and _SHARE to run")
	}

	fs, err := NewSMBFS(config.RemoteFSConfig{
		Host: host, Username: user, Password: pass, BasePath: share,
	})
	if err != nil {
		t.Fatalf("NewSMBFS: %v", err)
	}
	defer fs.Close()
	ctx := context.Background()
	t.Logf("backend concurrency: %d", fs.ListConcurrency())

	root, err := fs.List(ctx, ".")
	if err != nil {
		t.Fatalf("List root: %v", err)
	}
	dirs := []string{"."}
	for _, e := range root {
		if e.IsDir && len(dirs) < 16 { // bound the test's runtime
			dirs = append(dirs, e.Name)
		}
	}

	// Serial baseline per directory. Entry counts are compared rather than
	// names: enough to catch response desync, tolerant of "." vs name forms.
	baseline := make(map[string]int, len(dirs))
	for _, d := range dirs {
		entries, err := fs.List(ctx, d)
		if err != nil {
			t.Fatalf("baseline List %s: %v", d, err)
		}
		baseline[d] = len(entries)
	}

	// Concurrent pass: ListByHandle with a nil handle must behave exactly
	// like List. A live directory could gain/lose entries between passes and
	// flake the count check; prefer a quiet tree for this test.
	const workers = 8
	const rounds = 4
	errs := make(chan error, workers*rounds*len(dirs))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				for _, d := range dirs {
					entries, err := fs.ListByHandle(ctx, d, nil)
					if err != nil {
						errs <- fmt.Errorf("ListByHandle %s: %w", d, err)
						continue
					}
					if len(entries) != baseline[d] {
						errs <- fmt.Errorf("ListByHandle %s: got %d entries, baseline %d", d, len(entries), baseline[d])
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

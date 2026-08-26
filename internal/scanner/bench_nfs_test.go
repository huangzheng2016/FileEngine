package scanner

import (
	"context"
	"os"
	"testing"
	"time"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// TestNFSBench measures end-to-end scan throughput against a real NFS export
// (concurrent walk, handle chaining, connection pool). It is a manual
// diagnostic: all connection details come from the environment so nothing
// network-specific lives in the repo.
//
//	FE_BENCH_NFS_HOST=nas.tailnet FE_BENCH_NFS_EXPORT=/export \
//	FE_BENCH_NFS_ROOT=subdir FE_BENCH_DUR=5m \
//	go test ./internal/scanner -run TestNFSBench -v
func TestNFSBench(t *testing.T) {
	host := os.Getenv("FE_BENCH_NFS_HOST")
	export := os.Getenv("FE_BENCH_NFS_EXPORT")
	root := os.Getenv("FE_BENCH_NFS_ROOT")
	if host == "" || export == "" || root == "" {
		t.Skip("set FE_BENCH_NFS_HOST, FE_BENCH_NFS_EXPORT and FE_BENCH_NFS_ROOT to run")
	}
	dur := 5 * time.Minute
	if v := os.Getenv("FE_BENCH_DUR"); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			dur = parsed
		}
	}

	fs, err := remotefs.NewNFSFS(config.RemoteFSConfig{Host: host, BasePath: export})
	if err != nil {
		t.Fatalf("NewNFSFS: %v", err)
	}
	defer fs.Close()
	t.Logf("backend concurrency: %d", fs.ListConcurrency())

	repo := setupTestDB(t)
	session := &db.ScanSession{ID: 1, RootPath: root, Status: "scanning"}
	if err := repo.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()
	start := time.Now()
	sc := New(fs, repo, "", nil)
	err = sc.Scan(ctx, session)
	el := time.Since(start)
	if err != nil && ctx.Err() == nil {
		t.Fatalf("Scan: %v", err)
	}

	// Progress lines in the log carry the rate; here the final tally.
	var n int64
	if err := repo.DB().Model(&db.FileEntry{}).
		Where("scan_session_id = ?", 1).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	t.Logf("elapsed=%s completed=%v total_entries=%d (%.0f entries/min)",
		el.Round(time.Second), ctx.Err() == nil, n, float64(n)/el.Minutes())
	if ctx.Err() != nil {
		t.Skipf("timed out before completion (rate visible in progress logs): %v", ctx.Err())
	}
}

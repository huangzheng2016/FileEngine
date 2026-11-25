package scanner

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/remotefs"
)

// deadFS lists like the local disk but every file read times out, like a
// share that went away after the walk.
type deadFS struct {
	*remotefs.LocalFS
	reads atomic.Int64
}

func (d *deadFS) ReadFile(context.Context, string) (io.ReadCloser, error) {
	d.reads.Add(1)
	return nil, &remotefs.OpError{Protocol: "smb", Op: "open", Path: "p", Err: os.ErrDeadlineExceeded}
}

func TestScanPostPasses_StopAfterRepeatedTimeouts(t *testing.T) {
	prev := maxConsecutiveTimeouts
	maxConsecutiveTimeouts = 3
	defer func() { maxConsecutiveTimeouts = prev }()

	dirPath := t.TempDir()
	const n = 200
	for i := range n {
		writeFile(t, dirPath, fmt.Sprintf("p/%03d.jpg", i), []byte("same"), time.Time{})
	}
	local, err := remotefs.NewLocalFS(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	fs := &deadFS{LocalFS: local}
	repo := setupFileTestDB(t)
	_, session := scanSession(t, repo, fs, ".", func(s *model.ScanSession) {
		s.ExtractMetadata = true
		s.DetectDuplicates = true
	})

	if session.Status != "scanned" {
		t.Fatalf("status = %q, want scanned", session.Status)
	}
	for _, want := range []string{"已停止读取拍摄信息", "已停止查找重复文件"} {
		if !strings.Contains(session.ScanWarning, want) {
			t.Errorf("warning %q lacks %q", session.ScanWarning, want)
		}
	}
	row, err := repo.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.ScanWarning != session.ScanWarning {
		t.Errorf("stored warning %q, want %q", row.ScanWarning, session.ScanWarning)
	}
	// Both passes stop within a few in-flight reads of the limit.
	if r := fs.reads.Load(); r >= n {
		t.Errorf("%d reads for %d files in two passes: the passes did not stop", r, n)
	}
	f := entry(t, repo, session.ID, "p/000.jpg")
	if f.DateSource != "" || f.Hash != "" || f.DuplicateOf != nil {
		t.Errorf("timed-out file: source=%q hash=%q dup=%v, want untouched for the next scan", f.DateSource, f.Hash, f.DuplicateOf)
	}
}

package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"FileEngine/internal/db"
)

// Batch failures are recorded (concurrently, from workers) and Failures
// returns an independent copy.
func TestAutoTagAgentFailuresCollection(t *testing.T) {
	a := &AutoTagAgent{}

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				a.noteFailure(BatchFailure{
					BatchIndex: workerID*100 + i,
					Target:     fmt.Sprintf("dir-%d-%d", workerID, i),
					Err:        "boom",
				})
			}
		}(w)
	}
	wg.Wait()

	got := a.Failures()
	if len(got) != 100 {
		t.Fatalf("failures: got %d, want 100", len(got))
	}

	// Mutating the returned slice must not affect the agent's own state.
	got[0].Err = "mutated"
	if a.Failures()[0].Err == "mutated" {
		t.Fatal("Failures must return a copy")
	}
}

// The report section carries the failure count and per-batch reasons, and
// caps very long lists so the report stays readable.
func TestFailureReportSection(t *testing.T) {
	if got := failureReportSection(nil); got != "" {
		t.Fatalf("no failures: got %q, want empty", got)
	}

	failures := []BatchFailure{
		{BatchIndex: 3, Target: "incoming/project", Err: "context deadline exceeded"},
		{BatchIndex: 7, Target: "10 files", Err: "model overloaded"},
	}
	out := failureReportSection(failures)
	for _, want := range []string{"失败批次（2）", "批次 3（incoming/project）: context deadline exceeded", "批次 7（10 files）: model overloaded"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}

	var many []BatchFailure
	for i := 0; i < 25; i++ {
		many = append(many, BatchFailure{BatchIndex: i + 1, Target: fmt.Sprintf("dir-%d", i), Err: "boom"})
	}
	out = failureReportSection(many)
	if !strings.Contains(out, "失败批次（25）") {
		t.Fatalf("count header missing:\n%s", out)
	}
	if strings.Contains(out, "dir-24") || strings.Contains(out, "dir-20") {
		t.Fatal("failure list must be capped at 20 entries")
	}
	if !strings.Contains(out, "其余 5 个批次") {
		t.Fatalf("truncation note missing:\n%s", out)
	}
}

// The end-of-run re-sweep repeats the loose-file pass only while the untagged
// remainder keeps shrinking: it stops as soon as a round moves nothing (an
// agent that keeps missing the same files must not loop forever) and caps the
// extra rounds.
func TestRunRemainingSweeps(t *testing.T) {
	t.Run("no remainder: single pass, no sweeps", func(t *testing.T) {
		calls := 0
		runRemainingSweeps(2, func() { calls++ }, func() (int64, error) { return 0, nil })
		if calls != 1 {
			t.Fatalf("process calls: got %d, want 1", calls)
		}
	})

	t.Run("progress each round: sweeps up to the cap", func(t *testing.T) {
		calls := 0
		remaining := 30
		runRemainingSweeps(2, func() {
			calls++
			remaining -= 10
		}, func() (int64, error) { return int64(remaining), nil })
		// First pass + 2 extra sweeps, then maxExtra caps the loop even though
		// the remainder (0) was reached exactly on the last allowed sweep.
		if calls != 3 {
			t.Fatalf("process calls: got %d, want 3 (1 pass + 2 sweeps)", calls)
		}
	})

	t.Run("flat remainder stops early", func(t *testing.T) {
		calls := 0
		runRemainingSweeps(2, func() { calls++ }, func() (int64, error) { return 7, nil })
		// First pass, then one sweep that moves nothing → stop before the cap.
		if calls != 2 {
			t.Fatalf("process calls: got %d, want 2 (stop on no progress)", calls)
		}
	})

	t.Run("count error after first pass skips sweeps", func(t *testing.T) {
		calls := 0
		runRemainingSweeps(2, func() { calls++ }, func() (int64, error) { return 0, fmt.Errorf("db down") })
		if calls != 1 {
			t.Fatalf("process calls: got %d, want 1", calls)
		}
	})
}

// A loose-file batch that returned success but left files untagged is a silent
// miss unless re-checked: recheckBatchTagged must record a BatchFailure naming
// the missed paths, and stay quiet when everything got tagged.
func TestRecheckBatchTagged(t *testing.T) {
	repo := setupToolTestRepo(t)

	mk := func(path string, tagged bool) db.FileEntry {
		f := &db.FileEntry{
			ScanSessionID: 1,
			OriginalPath:  path,
			Name:          filepath.Base(path),
			FileType:      "file",
			Tagged:        tagged,
		}
		if err := repo.CreateFile(f); err != nil {
			t.Fatalf("seed file %s: %v", path, err)
		}
		return *f
	}

	t.Run("all tagged: no failure", func(t *testing.T) {
		a := &AutoTagAgent{repo: repo}
		a.recheckBatchTagged(1, []db.FileEntry{mk("in/ok1.txt", true), mk("in/ok2.txt", true)})
		if got := a.Failures(); len(got) != 0 {
			t.Fatalf("failures: got %d, want 0: %+v", len(got), got)
		}
	})

	t.Run("misses recorded with paths", func(t *testing.T) {
		a := &AutoTagAgent{repo: repo}
		files := []db.FileEntry{
			mk("in/tagged.txt", true),
			mk("in/missed1.txt", false),
			mk("in/missed2.txt", false),
		}
		a.recheckBatchTagged(7, files)
		got := a.Failures()
		if len(got) != 1 {
			t.Fatalf("failures: got %d, want 1: %+v", len(got), got)
		}
		f := got[0]
		if f.BatchIndex != 7 || f.Target != "3 files" {
			t.Fatalf("failure identity: %+v", f)
		}
		for _, want := range []string{"未标记 2/3 个文件", "in/missed1.txt", "in/missed2.txt"} {
			if !strings.Contains(f.Err, want) {
				t.Fatalf("failure error missing %q: %s", want, f.Err)
			}
		}
		if strings.Contains(f.Err, "in/tagged.txt") {
			t.Fatalf("tagged file must not be named as missed: %s", f.Err)
		}
	})
}

// The reconciliation line splits the untagged remainder by entry type and only
// flags files (dirs/symlinks are expected deltas).
func TestUntaggedReconciliationSection(t *testing.T) {
	out := untaggedReconciliationSection(db.UntaggedBreakdown{Files: 3, Directories: 12, Symlinks: 2})
	for _, want := range []string{"打标对账", "文件 3 / 目录 12 / 软链 2", "漏标"} {
		if !strings.Contains(out, want) {
			t.Fatalf("reconciliation missing %q:\n%s", want, out)
		}
	}

	clean := untaggedReconciliationSection(db.UntaggedBreakdown{Directories: 5, Symlinks: 1})
	if !strings.Contains(clean, "文件 0 / 目录 5 / 软链 1") {
		t.Fatalf("counts missing:\n%s", clean)
	}
	if strings.Contains(clean, "漏标") {
		t.Fatalf("zero untagged files must not raise the miss warning:\n%s", clean)
	}
}

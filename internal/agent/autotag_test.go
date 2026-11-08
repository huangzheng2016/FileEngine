package agent

import (
	"fmt"
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

func TestFailureReportSection(t *testing.T) {
	if got := failureReportSection(nil); got != "" {
		t.Fatalf("no failures: got %q, want empty", got)
	}
	out := failureReportSection([]BatchFailure{
		{Target: "incoming/project", Err: "context deadline exceeded"},
		{Target: "10 个文件 · misc", Err: "完成但遗漏 2/10 个条目未决定"},
	})
	for _, want := range []string{"失败与遗漏的任务（2）", "**incoming/project**：context deadline exceeded", "遗漏 2/10"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
	var many []BatchFailure
	for i := 0; i < 25; i++ {
		many = append(many, BatchFailure{Target: fmt.Sprintf("dir-%d", i), Err: "boom"})
	}
	out = failureReportSection(many)
	if !strings.Contains(out, "（25）") || strings.Contains(out, "dir-20") || !strings.Contains(out, "其余 5 个") {
		t.Fatalf("capped list:\n%s", out)
	}
}

func TestUntaggedReconciliationSection(t *testing.T) {
	out := untaggedReconciliationSection(db.UntaggedBreakdown{Files: 3, Directories: 12, Symlinks: 2}, 0)
	for _, want := range []string{"决定对账", "文件 3 / 目录 12 / 软链 2", "遗漏"} {
		if !strings.Contains(out, want) {
			t.Fatalf("reconciliation missing %q:\n%s", want, out)
		}
	}
	clean := untaggedReconciliationSection(db.UntaggedBreakdown{Symlinks: 1}, 7)
	if strings.Contains(clean, "遗漏") || !strings.Contains(clean, "系统垃圾已自动跳过 7 项") {
		t.Fatalf("clean run:\n%s", clean)
	}
}

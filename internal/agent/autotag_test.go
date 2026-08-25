package agent

import (
	"fmt"
	"strings"
	"sync"
	"testing"
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

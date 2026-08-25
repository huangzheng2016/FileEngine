package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

// Harness pull semantics: list_files with tagged=false under a harness
// builder is sharded to the worker (id % numWorkers = workerID) and rolls a
// fresh batch; non-pull queries (tagged=true, or no harness) are untouched.
func TestListFilesHarnessShardAndBatchRoll(t *testing.T) {
	repo := setupToolTestRepo(t)
	for _, p := range []string{"f/1.txt", "f/2.txt", "f/3.txt", "f/4.txt"} {
		seedFile(t, repo, 1, p, "file")
	}
	var counter int32
	logger := NewLogger(repo, 1)
	tb := NewToolBuilder(repo, nil, 1, 1, logger, config.AgentConfig{}, &db.ScanSession{}, false).
		withHarness(1, 2, &counter)

	falseVal := false
	out, err := tb.listFiles(context.Background(), &ListFilesInput{FileType: "file", Tagged: &falseVal, Limit: 10})
	if err != nil {
		t.Fatalf("listFiles: %v", err)
	}
	// Worker 1 of 2: only odd-id rows. 4 seeded files → 2 in this shard.
	if out.Total != 2 {
		t.Fatalf("sharded total = %d, want 2", out.Total)
	}
	if atomic.LoadInt32(&counter) != 1 {
		t.Fatalf("batch counter = %d, want 1 (one batch rolled)", counter)
	}
	if logger.GetBatch() != 1 {
		t.Fatalf("logger batch = %d, want 1", logger.GetBatch())
	}

	// Second pull (queue unchanged in this test) rolls the next batch.
	if _, err := tb.listFiles(context.Background(), &ListFilesInput{FileType: "file", Tagged: &falseVal, Limit: 10}); err != nil {
		t.Fatalf("listFiles 2: %v", err)
	}
	if atomic.LoadInt32(&counter) != 2 {
		t.Fatalf("batch counter = %d, want 2", counter)
	}

	// Non-pull query (tagged=true): no shard, no batch roll.
	trueVal := true
	if _, err := tb.listFiles(context.Background(), &ListFilesInput{Tagged: &trueVal, Limit: 10}); err != nil {
		t.Fatalf("listFiles tagged: %v", err)
	}
	if atomic.LoadInt32(&counter) != 2 {
		t.Fatalf("tagged=true pull must not roll batches, counter = %d", counter)
	}

	// No-harness builder: no shard (sees all 4), no roll.
	plain := NewToolBuilder(repo, nil, 1, 1, NewLogger(repo, 1), config.AgentConfig{}, &db.ScanSession{}, false)
	pout, err := plain.listFiles(context.Background(), &ListFilesInput{FileType: "file", Tagged: &falseVal, Limit: 10})
	if err != nil {
		t.Fatalf("plain listFiles: %v", err)
	}
	if pout.Total != 4 {
		t.Fatalf("unsharded total = %d, want 4", pout.Total)
	}
}

func TestBuildHarnessPrompt(t *testing.T) {
	out := buildHarnessPrompt("\n## 分类体系\n")
	for _, want := range []string{"循环", "tagged=false", "mark_tagged", "total=0", "分类体系"} {
		if !strings.Contains(out, want) {
			t.Fatalf("harness prompt missing %q:\n%s", want, out)
		}
	}
}

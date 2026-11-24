package db

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"FileEngine/biz/dal/db/model"
)

func countEvents(t *testing.T, r *Repository, sessionID uint, typ string) int {
	t.Helper()
	rows, _, err := r.ListEvents(EventQuery{SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range rows {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

func TestConcurrentStartsOfOneSessionYieldOneRunningRun(t *testing.T) {
	r := setupScanProgressRepo(t)
	s := &model.ScanSession{Status: "scanned"}
	if err := r.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	var won, busy atomic.Int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch _, err := r.CreateRun(s.ID, RunPhaseTagging, "整理"); {
			case err == nil:
				won.Add(1)
			case errors.Is(err, ErrSessionBusy):
				busy.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 || busy.Load() != n-1 {
		t.Fatalf("won=%d busy=%d", won.Load(), busy.Load())
	}
}

func TestSetRunCountersAreStoredOnTheRun(t *testing.T) {
	r := setupScanProgressRepo(t)
	run, err := r.CreateRun(32, RunPhaseExecuting, "执行")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetRunCounters(run.ID, 10, 7, 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.FinishRun(run, RunPartial, "", "", TokenCounts{}); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetRun(run.ID)
	if err != nil || got.ExecTotal != 10 || got.ExecSuccess != 7 || got.ExecFailed != 2 || got.ExecSkipped != 1 {
		t.Fatalf("counters = %+v (%v)", got, err)
	}
}

func TestFinishRunTwiceEmitsOnce(t *testing.T) {
	r := setupScanProgressRepo(t)
	run, err := r.CreateRun(31, RunPhaseTagging, "整理")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.FinishRun(run, RunDone, "ok", "", TokenCounts{}); err != nil {
		t.Fatal(err)
	}
	if err := r.FinishRun(run, RunStopped, "", "", TokenCounts{}); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, r, 31, EvRunDone); n != 1 {
		t.Fatalf("run_done events = %d, want 1", n)
	}
	// start plus the terminal status of the first finish only
	if n := countEvents(t, r, 31, EvRunStatus); n != 2 {
		t.Fatalf("run_status events = %d, want 2", n)
	}
	got, err := r.GetRun(run.ID)
	if err != nil || got.Status != RunDone {
		t.Fatalf("stored run = %+v (%v)", got, err)
	}
}

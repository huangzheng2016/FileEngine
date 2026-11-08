package db

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func recvEvent(t *testing.T, s *Subscriber) SessionEvent {
	t.Helper()
	select {
	case ev := <-s.C:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
	return SessionEvent{}
}

func TestEmitPersistsAndBroadcastsInSeqOrder(t *testing.T) {
	r := setupScanProgressRepo(t)
	sub := Subscribe(7)
	defer Unsubscribe(sub)

	Emit(7, 1, EvRunStatus, RunStatusData{RunID: 1, Phase: RunPhaseTagging, Status: RunRunning})
	Emit(7, 1, EvProgress, ProgressData{RunID: 1, Phase: RunPhaseTagging, Total: 3})
	Emit(8, 2, EvProgress, ProgressData{RunID: 2, Phase: RunPhaseTagging, Total: 9}) // other session

	a, b := recvEvent(t, sub), recvEvent(t, sub)
	if a.Type != EvRunStatus || b.Type != EvProgress || a.ID == 0 || b.ID <= a.ID {
		t.Fatalf("got %s#%d then %s#%d", a.Type, a.ID, b.Type, b.ID)
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("event of another session leaked: %+v", ev)
	default:
	}

	rows, more, err := r.ListEvents(EventQuery{SessionID: 7})
	if err != nil || more || len(rows) != 2 || rows[0].ID != a.ID || rows[1].ID != b.ID {
		t.Fatalf("persisted rows = %+v more=%v err=%v", rows, more, err)
	}
	var p ProgressData
	if err := json.Unmarshal([]byte(rows[1].Data), &p); err != nil || p.Total != 3 {
		t.Fatalf("payload = %s (%v)", rows[1].Data, err)
	}
}

// Deltas of one message coalesce into one chunk, and a non-delta event of the
// same run flushes pending chunks first so persisted order = emission order.
func TestDeltaCoalescingAndOrdering(t *testing.T) {
	r := setupScanProgressRepo(t)
	const sid, run = 11, 5

	Emit(sid, run, EvReasoning, DeltaData{WorkerID: 0, MsgID: "m1", Delta: "think "})
	Emit(sid, run, EvReasoning, DeltaData{WorkerID: 0, MsgID: "m1", Delta: "more"})
	Emit(sid, run, EvText, DeltaData{WorkerID: 0, MsgID: "m1", Delta: "Hel"}) // type switch flushes reasoning
	Emit(sid, run, EvText, DeltaData{WorkerID: 0, MsgID: "m1", Delta: "lo"})
	Emit(sid, run, EvText, DeltaData{WorkerID: 1, MsgID: "m2", Delta: "other lane"})
	Emit(sid, run, EvMessage, MessageData{WorkerID: 0, MsgID: "m1", Role: "assistant", Content: "Hello"})

	rows, _, err := r.ListEvents(EventQuery{SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ev := range rows {
		var d DeltaData
		_ = json.Unmarshal([]byte(ev.Data), &d)
		got = append(got, ev.Type+":"+d.Delta)
		if ev.RunID != run {
			t.Errorf("run id = %d", ev.RunID)
		}
	}
	want := []string{"reasoning:think more", "text:Hello", "text:other lane", "message:"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestDeltaFlushByTimerAndSize(t *testing.T) {
	r := setupScanProgressRepo(t)
	Emit(12, 1, EvText, DeltaData{MsgID: "a", Delta: "tick"})
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, _, _ := r.ListEvents(EventQuery{SessionID: 12})
		if len(rows) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending delta was never flushed by the timer")
		}
		time.Sleep(20 * time.Millisecond)
	}

	big := strings.Repeat("x", deltaFlushBytes)
	Emit(13, 1, EvText, DeltaData{MsgID: "b", Delta: big})
	rows, _, _ := r.ListEvents(EventQuery{SessionID: 13})
	if len(rows) != 1 {
		t.Fatalf("a delta of %d bytes must flush immediately, got %d rows", len(big), len(rows))
	}
}

func TestListEventsPaging(t *testing.T) {
	r := setupScanProgressRepo(t)
	for i := 0; i < 5; i++ {
		Emit(20, uint(1+i%2), EvProgress, ProgressData{Total: i})
	}
	all, _, _ := r.ListEvents(EventQuery{SessionID: 20})
	if len(all) != 5 {
		t.Fatalf("got %d events", len(all))
	}
	page, more, _ := r.ListEvents(EventQuery{SessionID: 20, After: all[0].ID, Limit: 2})
	if len(page) != 2 || !more || page[0].ID != all[1].ID {
		t.Fatalf("after page = %+v more=%v", page, more)
	}
	back, more, _ := r.ListEvents(EventQuery{SessionID: 20, Before: all[4].ID, Limit: 2})
	if len(back) != 2 || !more || back[0].ID != all[2].ID || back[1].ID != all[3].ID {
		t.Fatalf("before page = %+v more=%v", back, more)
	}
	byRun, _, _ := r.ListEvents(EventQuery{SessionID: 20, RunID: 2})
	if len(byRun) != 2 {
		t.Fatalf("run filter returned %d", len(byRun))
	}
}

func TestRunLifecycle(t *testing.T) {
	r := setupScanProgressRepo(t)
	run, err := r.CreateRun(30, RunPhaseTagging, "整理")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateRun(30, RunPhaseChat, "对话"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("second run: err = %v, want ErrSessionBusy", err)
	}
	if err := r.FinishRun(run, RunPartial, "报告", "", TokenCounts{Prompt: 10, Completion: 5, Total: 15}); err != nil {
		t.Fatal(err)
	}
	next, err := r.CreateRun(30, RunPhaseExecuting, "执行")
	if err != nil {
		t.Fatalf("run after finish: %v", err)
	}
	if err := r.FinishRun(next, RunDone, "", "", TokenCounts{Total: 1}); err != nil {
		t.Fatal(err)
	}
	tok, err := r.SessionTokens(30)
	if err != nil || tok.Total != 16 || tok.Prompt != 10 {
		t.Fatalf("session tokens = %+v (%v)", tok, err)
	}

	start, err := r.LatestRunStartSeq(30)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, _ := r.ListEvents(EventQuery{SessionID: 30, After: start})
	if len(rows) == 0 || rows[0].RunID != next.ID || rows[0].Type != EvRunStatus {
		t.Fatalf("latest run replay starts at %+v", rows)
	}
	last := rows[len(rows)-1]
	if last.Type != EvRunDone {
		t.Fatalf("last event = %s, want run_done", last.Type)
	}
}

func TestSweepStaleRuns(t *testing.T) {
	r := setupScanProgressRepo(t)
	run, err := r.CreateRun(40, RunPhaseTagging, "整理")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.SweepStaleRuns(); err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	got, _ := r.GetRun(run.ID)
	if got.Status != RunStopped || got.Error != RunInterruptedError || got.EndedAt == nil {
		t.Fatalf("swept run = %+v", got)
	}
}

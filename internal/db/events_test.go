package db

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"FileEngine/biz/dal/db/model"
)

func recvEvent(t *testing.T, s *Subscriber) model.SessionEvent {
	t.Helper()
	select {
	case ev := <-s.C:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
	return model.SessionEvent{}
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

	rows := eventsOf(t, r, 7)
	if len(rows) != 2 || rows[0].ID != a.ID || rows[1].ID != b.ID {
		t.Fatalf("persisted rows = %+v", rows)
	}
	var p ProgressData
	if err := json.Unmarshal([]byte(rows[1].Data), &p); err != nil || p.Total != 3 {
		t.Fatalf("payload = %s (%v)", rows[1].Data, err)
	}
}

func TestDeltaCoalescingAndOrdering(t *testing.T) {
	r := setupScanProgressRepo(t)
	const sid, run = 11, 5

	Emit(sid, run, EvReasoning, DeltaData{WorkerID: 0, MsgID: "m1", Delta: "think "})
	Emit(sid, run, EvReasoning, DeltaData{WorkerID: 0, MsgID: "m1", Delta: "more"})
	Emit(sid, run, EvText, DeltaData{WorkerID: 0, MsgID: "m1", Delta: "Hel"}) // type switch flushes reasoning
	Emit(sid, run, EvText, DeltaData{WorkerID: 0, MsgID: "m1", Delta: "lo"})
	Emit(sid, run, EvText, DeltaData{WorkerID: 1, MsgID: "m2", Delta: "other lane"})
	Emit(sid, run, EvMessage, MessageData{WorkerID: 0, MsgID: "m1", Role: "assistant", Content: "Hello"})

	rows := eventsOf(t, r, sid)
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
		rows := eventsOf(t, r, 12)
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
	rows := eventsOf(t, r, 13)
	if len(rows) != 1 {
		t.Fatalf("a delta of %d bytes must flush immediately, got %d rows", len(big), len(rows))
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
	all := eventsOf(t, r, 30)
	var rows []model.SessionEvent
	for _, ev := range all {
		if ev.RunID == next.ID {
			rows = append(rows, ev)
		}
	}
	if len(rows) == 0 || rows[0].Type != EvRunStatus {
		t.Fatalf("latest run events start at %+v", rows)
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
	got := getRun(t, r, run.ID)
	if got.Status != RunStopped || got.Error != RunInterruptedError || got.EndedAt == nil {
		t.Fatalf("swept run = %+v", got)
	}
}

func eventsOf(t *testing.T, r *Repository, sessionID uint) []model.SessionEvent {
	t.Helper()
	var rows []model.SessionEvent
	if err := r.DB().Where("session_id = ?", sessionID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

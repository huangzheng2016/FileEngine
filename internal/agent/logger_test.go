package agent

import (
	"testing"

	"FileEngine/internal/db"
)

func TestLoggerEmitsRunScopedEvents(t *testing.T) {
	setupToolTestRepo(t)
	const sid = 100
	sub := db.Subscribe(sid)
	defer db.Unsubscribe(sub)

	l := NewLogger(sid, 4, 2)
	l.EmitPullBegin("照片", 3)
	l.LogMessage("user", "hello")
	l.EmitPullDone(2, 1)
	l.EmitPullDone(0, 0) // no open unit: ignored
	l.EmitProgress(db.ProgressData{Phase: db.RunPhaseTagging, Total: 10, Tagged: 2})

	evs := collectEvents(t, sub)
	if len(evs) != 4 {
		t.Fatalf("got %d events: %+v", len(evs), evs)
	}
	begin, msg, done, prog := evs[0], evs[1], evs[2], evs[3]
	if begin.Type != db.EvPullBegin || begin.Data["label"] != "照片" || begin.Data["count"] != float64(3) || begin.Data["unit_id"] == "" {
		t.Fatalf("pull_begin: %+v", begin)
	}
	if done.Type != db.EvPullDone || done.Data["unit_id"] != begin.Data["unit_id"] || done.Data["tagged"] != float64(2) || done.Data["failed"] != float64(1) {
		t.Fatalf("pull_done: %+v", done)
	}
	if msg.Type != db.EvMessage || msg.Data["role"] != "user" || msg.Data["content"] != "hello" || msg.Data["msg_id"] == "" {
		t.Fatalf("message: %+v", msg)
	}
	if prog.Type != db.EvProgress || prog.Data["run_id"] != float64(4) || prog.Data["total"] != float64(10) {
		t.Fatalf("progress: %+v", prog)
	}
	for _, e := range evs[:3] {
		if e.Data["run_id"] != float64(4) || e.Data["worker_id"] != float64(2) {
			t.Fatalf("%s lacks run/worker ids: %v", e.Type, e.Data)
		}
	}
}

// Ids stay unique across all lanes of a run, including loggers recreated for
// the same worker (e.g. one per depth level).
func TestLoggerIDsUniqueWithinRun(t *testing.T) {
	root := NewLogger(1, 1, 0)
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		for _, l := range []*Logger{root.ForWorker(1), root.ForWorker(1), root.ForWorker(2)} {
			id := l.NewMsgID()
			if seen[id] {
				t.Fatalf("duplicate msg id %q", id)
			}
			seen[id] = true
		}
	}
}

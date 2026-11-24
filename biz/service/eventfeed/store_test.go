package eventfeed

import (
	"context"
	"strings"
	"testing"

	"FileEngine/biz/dal/db/dbtest"
	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
)

func seedEvents(t *testing.T, q *query.Query, sessionID uint, runs []uint) []model.SessionEvent {
	t.Helper()
	rows := make([]*model.SessionEvent, len(runs))
	for i, r := range runs {
		rows[i] = &model.SessionEvent{SessionID: sessionID, RunID: r, Type: "progress", Data: "{}"}
	}
	if err := q.SessionEvent.WithContext(context.Background()).Create(rows...); err != nil {
		t.Fatal(err)
	}
	out := make([]model.SessionEvent, len(rows))
	for i, r := range rows {
		out[i] = *r
	}
	return out
}

func TestListPaging(t *testing.T) {
	q := dbtest.New(t)
	ctx := context.Background()
	all := seedEvents(t, q, 20, []uint{1, 2, 1, 2, 1})
	seedEvents(t, q, 21, []uint{1})

	got, more, err := List(ctx, q, ListQuery{SessionID: 20})
	if err != nil || more || len(got) != 5 {
		t.Fatalf("all = %d more=%v err=%v", len(got), more, err)
	}
	page, more, _ := List(ctx, q, ListQuery{SessionID: 20, After: all[0].ID, Limit: 2})
	if len(page) != 2 || !more || page[0].ID != all[1].ID {
		t.Fatalf("after page = %+v more=%v", page, more)
	}
	back, more, _ := List(ctx, q, ListQuery{SessionID: 20, Before: all[4].ID, Limit: 2})
	if len(back) != 2 || !more || back[0].ID != all[2].ID || back[1].ID != all[3].ID {
		t.Fatalf("before page = %+v more=%v", back, more)
	}
	byRun, _, _ := List(ctx, q, ListQuery{SessionID: 20, RunID: 2})
	if len(byRun) != 2 {
		t.Fatalf("run filter returned %d", len(byRun))
	}
}

func TestLatestRunStart(t *testing.T) {
	q := dbtest.New(t)
	ctx := context.Background()
	if n, err := latestRunStart(ctx, q, 30); err != nil || n != 0 {
		t.Fatalf("no run: %d %v", n, err)
	}
	r1 := &model.AgentRun{SessionID: 30, Phase: "tagging", Status: "done"}
	r2 := &model.AgentRun{SessionID: 30, Phase: "chat", Status: "done"}
	if err := q.AgentRun.WithContext(ctx).Create(r1, r2); err != nil {
		t.Fatal(err)
	}
	ev := seedEvents(t, q, 30, []uint{r1.ID, r2.ID, r2.ID})
	if n, err := latestRunStart(ctx, q, 30); err != nil || n != ev[1].ID-1 {
		t.Fatalf("start = %d (%v), want %d", n, err, ev[1].ID-1)
	}
}

// Replay without a run filter reads session_id = ? AND id > ? ORDER BY id; it
// must come from an index ordered by (session_id, id), not a scan of every
// event after the cursor.
func TestEventReplayUsesSessionIndex(t *testing.T) {
	q := dbtest.New(t)
	// One small session among large ones: without an index ordered by
	// (session_id, id) the cursor scan walks every later event of the others.
	for s, n := range map[uint]int{1: 30, 2: 1500, 3: 1500} {
		runs := make([]uint, n)
		for i := range runs {
			runs[i] = uint(1 + i%3)
		}
		seedEvents(t, q, s, runs)
	}
	g := q.SessionEvent.WithContext(context.Background()).UnderlyingDB()
	if err := g.Exec("ANALYZE").Error; err != nil {
		t.Fatal(err)
	}
	var plan []struct {
		ID, Parent, Notused int
		Detail              string
	}
	if err := g.Raw("EXPLAIN QUERY PLAN SELECT * FROM session_events WHERE session_id = 1 AND id > 5 ORDER BY id LIMIT 501").Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, p := range plan {
		sb.WriteString(p.Detail + "\n")
	}
	if !strings.Contains(sb.String(), "idx_event_session_seq") || strings.Contains(sb.String(), "TEMP B-TREE") {
		t.Fatalf("replay plan:\n%s", sb.String())
	}
}

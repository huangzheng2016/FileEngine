package db

import (
	"testing"
	"time"
)

func TestReviewCountAndSessionUpdatedEvent(t *testing.T) {
	r := setupScanProgressRepo(t)
	old := countsDelay
	countsDelay = 10 * time.Millisecond
	defer func() { countsDelay = old }()

	sess := &ScanSession{Status: "scanned"}
	if err := r.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	files := []FileEntry{
		{ScanSessionID: sess.ID, OriginalPath: "a", Name: "a", FileType: "file", Operation: "planned", NewPath: "/x/a", NeedsReview: true},
		{ScanSessionID: sess.ID, OriginalPath: "b", Name: "b", FileType: "file", Operation: "planned", NewPath: "/x/b", NeedsReview: true},
		{ScanSessionID: sess.ID, OriginalPath: "c", Name: "c", FileType: "file", Operation: "planned", NewPath: "/x/c"},
		{ScanSessionID: sess.ID, OriginalPath: "d", Name: "d", FileType: "file"},
	}
	if err := r.CreateFiles(files); err != nil {
		t.Fatal(err)
	}
	sub := Subscribe(sess.ID)
	defer Unsubscribe(sub)

	if err := r.RefreshSessionCounts(sess.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetSession(sess.ID)
	if got.ReviewCount != 2 || got.PlannedOps != 3 {
		t.Fatalf("review_count=%d planned_ops=%d, want 2/3", got.ReviewCount, got.PlannedOps)
	}
	if ev := recvEvent(t, sub); ev.Type != EvSessionUpdated || ev.Data != "{}" || ev.RunID != 0 {
		t.Fatalf("event after refresh = %+v", ev)
	}

	if err := r.RefreshSessionCounts(sess.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("unchanged counters must not emit: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	var a FileEntry
	if err := r.DB().Where("scan_session_id = ? AND original_path = ?", sess.ID, "a").First(&a).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := r.ConfirmReview(sess.ID, []uint{a.ID}); err != nil || n != 1 {
		t.Fatalf("confirm: %d %v", n, err)
	}
	if ev := recvEvent(t, sub); ev.Type != EvSessionUpdated {
		t.Fatalf("event after confirm = %+v", ev)
	}
	if got, _ := r.GetSession(sess.ID); got.ReviewCount != 1 {
		t.Fatalf("review_count after confirm = %d, want 1", got.ReviewCount)
	}
}

func TestLatestRuns(t *testing.T) {
	r := setupScanProgressRepo(t)
	a, b := &ScanSession{}, &ScanSession{}
	if err := r.CreateSession(a); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateSession(b); err != nil {
		t.Fatal(err)
	}
	first, err := r.CreateRun(a.ID, RunPhaseTagging, "整理")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.FinishRun(first, RunDone, "", "", TokenCounts{}); err != nil {
		t.Fatal(err)
	}
	second, err := r.CreateRun(a.ID, RunPhaseReview, "复查")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := r.LatestRuns([]uint{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := latest[a.ID]; got != (RunRef{ID: second.ID, Phase: RunPhaseReview, Status: RunRunning}) {
		t.Fatalf("latest of a = %+v", got)
	}
	if _, ok := latest[b.ID]; ok {
		t.Fatal("session without runs must have no latest run")
	}
}

package run

import (
	"context"
	"errors"
	"testing"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/dbtest"
	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/db"
)

func TestListAndGetFillRevertedBy(t *testing.T) {
	ctx := context.Background()
	q := dbtest.New(t)
	s := &Service{q: q, rq: &dal.Reader{Query: q}}
	sess := &model.ScanSession{Status: "scanned"}
	if err := q.ScanSession.WithContext(ctx).Create(sess); err != nil {
		t.Fatal(err)
	}
	exec := &model.AgentRun{SessionID: sess.ID, Phase: db.RunPhaseExecuting, Status: db.RunDone}
	tagging := &model.AgentRun{SessionID: sess.ID, Phase: db.RunPhaseTagging, Status: db.RunDone}
	if err := q.AgentRun.WithContext(ctx).Create(exec, tagging); err != nil {
		t.Fatal(err)
	}
	failed := &model.AgentRun{SessionID: sess.ID, Phase: db.RunPhaseExecuting, Status: db.RunError, RevertsRunID: &exec.ID}
	revert := &model.AgentRun{SessionID: sess.ID, Phase: db.RunPhaseExecuting, Status: db.RunDone, RevertsRunID: &exec.ID}
	if err := q.AgentRun.WithContext(ctx).Create(failed, revert); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, exec.ID)
	if err != nil || got.RevertedByRunID == nil || *got.RevertedByRunID != revert.ID {
		t.Fatalf("Get: %+v %v (a failed revert must not count)", got, err)
	}
	list, err := s.List(ctx, sess.ID)
	if err != nil || len(list) != 4 || list[0].ID != revert.ID {
		t.Fatalf("List must be newest first: %+v %v", list, err)
	}
	for _, r := range list {
		if want := r.ID == exec.ID; want != (r.RevertedByRunID != nil) {
			t.Fatalf("run %d: reverted_by = %v", r.ID, r.RevertedByRunID)
		}
	}
	if _, err := s.Get(ctx, 9999); !errors.Is(err, ErrNoRun) {
		t.Fatalf("missing run: %v", err)
	}
}

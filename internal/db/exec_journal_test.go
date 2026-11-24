package db

import (
	"strings"
	"testing"
	"time"

	"FileEngine/biz/dal/db/model"

	"gorm.io/gorm"
)

func TestFindDoneExecOpUsesTargetPathIndex(t *testing.T) {
	r := setupBrainRepo(t)
	sql := r.db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		var ops []model.ExecOp
		return tx.Where("session_id = ? AND dst = ? AND state = ?", 1, "a/b", ExecDone).
			Order("id DESC").Limit(1).Find(&ops)
	})
	var plan []struct {
		ID, Parent, Notused int
		Detail              string
	}
	if err := r.db.Raw("EXPLAIN QUERY PLAN " + sql).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	var details []string
	for _, p := range plan {
		details = append(details, p.Detail)
	}
	if !strings.Contains(strings.Join(details, "\n"), "idx_execop_session_dst") {
		t.Fatalf("journal lookup by target path does not use its index: %v", details)
	}
}

func TestSetExecOpStateDstIsOneUpdate(t *testing.T) {
	r := setupBrainRepo(t)
	op := &model.ExecOp{RunID: 1, SessionID: 1, Kind: ExecKindMove, Src: "a", Dst: "b", State: ExecPending}
	if err := r.CreateExecOp(op); err != nil {
		t.Fatal(err)
	}
	mt := time.Unix(1700000000, 0)
	if err := r.SetExecOpStateDst(op, ExecVerified, "", 42, mt); err != nil {
		t.Fatal(err)
	}
	ops, _ := r.ListExecOps(1)
	if ops[0].State != ExecVerified || ops[0].DstSize != 42 || ops[0].DstModTime.Unix() != mt.Unix() {
		t.Fatalf("op = %+v", ops[0])
	}
	if err := r.SetExecOpCreated(op); err != nil {
		t.Fatal(err)
	}
	ops, _ = r.ListExecOps(1)
	if !ops[0].DstCreated {
		t.Fatal("DstCreated not persisted")
	}
}

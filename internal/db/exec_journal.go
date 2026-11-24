package db

import (
	"path"
	"strings"
	"time"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"

	"golang.org/x/text/unicode/norm"
)

// Execution journal states. An op is written as pending before its first
// side effect and advanced after each verified step, so a crash leaves enough
// to finish or roll back the op (see executor.Recover).
const (
	ExecPending  = "pending"  // about to act; dst may hold a partial copy (see DstCreated)
	ExecVerified = "verified" // dst verified; for moves the source is still there
	ExecDone     = "done"
	ExecFailed   = "failed"
	ExecReverted = "reverted"
)

const (
	ExecKindRename = "rename" // in-place rename of a file or a whole directory
	ExecKindCopy   = "copy"   // verified copy, source kept
	ExecKindMove   = "move"   // verified copy, then the source file is deleted
	ExecKindMkdir  = "mkdir"  // directory created at Dst
	ExecKindRmdir  = "rmdir"  // source directory Src removed after it became empty
)

func (r *Repository) CreateExecOp(op *model.ExecOp) error {
	return dal.WithRetry(func() error { return r.db.Create(op).Error })
}

func (r *Repository) SetExecOpState(op *model.ExecOp, state, errMsg string) error {
	op.State, op.Error = state, errMsg
	return dal.WithRetry(func() error {
		return r.db.Model(&model.ExecOp{}).Where("id = ?", op.ID).
			Updates(map[string]any{"state": state, "error": errMsg, "updated_at": time.Now()}).Error
	})
}

func (r *Repository) SetExecOpCreated(op *model.ExecOp) error {
	op.DstCreated = true
	return dal.WithRetry(func() error {
		return r.db.Model(&model.ExecOp{}).Where("id = ?", op.ID).
			Updates(map[string]any{"dst_created": true, "updated_at": time.Now()}).Error
	})
}

// SetExecOpStateDst advances the state and records the destination stat in
// one statement, so a crash never leaves a state without its stat.
func (r *Repository) SetExecOpStateDst(op *model.ExecOp, state, errMsg string, size int64, mtime time.Time) error {
	op.State, op.Error, op.DstSize, op.DstModTime = state, errMsg, size, mtime
	return dal.WithRetry(func() error {
		return r.db.Model(&model.ExecOp{}).Where("id = ?", op.ID).
			Updates(map[string]any{"state": state, "error": errMsg, "dst_size": size,
				"dst_mod_time": mtime, "updated_at": time.Now()}).Error
	})
}

func (r *Repository) ListExecOps(runID uint) ([]model.ExecOp, error) {
	var ops []model.ExecOp
	err := r.db.Where("run_id = ?", runID).Order("id ASC").Find(&ops).Error
	return ops, err
}

func (r *Repository) ListUnfinishedExecOps(sessionID uint) ([]model.ExecOp, error) {
	var ops []model.ExecOp
	err := r.db.Where("session_id = ? AND state IN ?", sessionID, []string{ExecPending, ExecVerified}).
		Order("id ASC").Find(&ops).Error
	return ops, err
}

// Finished single-step ops whose entry was never marked executed (a crash
// between the op's done state and MarkEntryExecuted).
func (r *Repository) ListDoneOpsOfPlannedEntries(sessionID uint) ([]model.ExecOp, error) {
	var ops []model.ExecOp
	err := r.db.Joins("JOIN file_entries ON file_entries.id = exec_ops.entry_id").
		Where("exec_ops.session_id = ? AND exec_ops.state = ? AND exec_ops.kind IN ? AND file_entries.executed = ?",
			sessionID, ExecDone, []string{ExecKindRename, ExecKindCopy, ExecKindMove}, false).
		Order("exec_ops.id ASC").Find(&ops).Error
	return ops, err
}

// The executor uses it to tell its own earlier output from a foreign file
// when resuming a partially executed directory.
func (r *Repository) FindDoneExecOp(sessionID uint, dst string) (*model.ExecOp, error) {
	var ops []model.ExecOp
	err := r.db.Where("session_id = ? AND dst = ? AND state = ?", sessionID, dst, ExecDone).
		Order("id DESC").Limit(1).Find(&ops).Error
	if err != nil || len(ops) == 0 {
		return nil, err
	}
	return &ops[0], nil
}

func (r *Repository) LatestExecOpByEntry(entryIDs []uint) (map[uint]model.ExecOp, error) {
	out := map[uint]model.ExecOp{}
	if len(entryIDs) == 0 {
		return out, nil
	}
	var ops []model.ExecOp
	if err := r.db.Where("entry_id IN ?", entryIDs).Order("id ASC").Find(&ops).Error; err != nil {
		return nil, err
	}
	for _, op := range ops {
		out[op.EntryID] = op
	}
	return out, nil
}

func (r *Repository) MarkEntryExecuted(id uint, mode string) error {
	now := time.Now()
	return dal.WithRetry(func() error {
		return r.db.Model(&model.FileEntry{}).Where("id = ?", id).
			Updates(map[string]any{"executed": true, "executed_at": &now, "execute_mode": mode}).Error
	})
}

func (r *Repository) UnmarkEntryExecuted(id uint) error {
	return dal.WithRetry(func() error {
		return r.db.Model(&model.FileEntry{}).Where("id = ?", id).
			Updates(map[string]any{"executed": false, "executed_at": nil, "execute_mode": ""}).Error
	})
}

func (r *Repository) ListPlannedEntries(sessionID uint) ([]model.FileEntry, error) {
	var files []model.FileEntry
	err := r.db.Where("scan_session_id = ? AND operation != '' AND new_path != '' AND executed = ?", sessionID, false).
		Order("original_path ASC, id ASC").Find(&files).Error
	return files, err
}

func (r *Repository) ListDecidedEntries(sessionID uint) ([]model.FileEntry, error) {
	var files []model.FileEntry
	err := r.db.Where("scan_session_id = ? AND operation != '' AND new_path != ''", sessionID).
		Order("original_path ASC, id ASC").Find(&files).Error
	return files, err
}

func (r *Repository) SearchEntries(sessionID uint, q string, limit int) ([]model.FileEntry, error) {
	var files []model.FileEntry
	q = strings.TrimPrefix(strings.TrimSpace(q), "/")
	pattern := "%" + dal.LikeLiteral(q) + "%"
	tx := r.db.Where("scan_session_id = ?", sessionID)
	err := tx.Where(r.db.Where("original_path = ?", q).
		Or(dal.CILikeCondition(r.db, "original_path"), pattern).
		Or(nameSearchCondition(r.db), pattern)).
		Order("depth ASC, original_path ASC").Limit(limit).Find(&files).Error
	return files, err
}

// Two targets with the same key collide on case- or normalization-insensitive
// filesystems (SMB, macOS) even if their bytes differ.
func CollisionKey(p string) string {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	return strings.ToLower(norm.NFC.String(p))
}

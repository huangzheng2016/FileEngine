package db

import (
	"path"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

// Execution journal states. An op is written as pending before its first
// side effect and advanced after each verified step, so a crash leaves enough
// to finish or roll back the op (see executor.Recover).
const (
	ExecPending  = "pending"  // about to act; dst may hold a partial copy
	ExecCopied   = "copied"   // dst written, not yet verified
	ExecVerified = "verified" // dst verified; for moves the source is still there
	ExecDone     = "done"
	ExecFailed   = "failed"
	ExecSkipped  = "skipped"
	ExecReverted = "reverted"
)

// Execution journal op kinds.
const (
	ExecKindRename = "rename" // in-place rename of a file or a whole directory
	ExecKindCopy   = "copy"   // verified copy, source kept
	ExecKindMove   = "move"   // verified copy, then the source file is deleted
	ExecKindMkdir  = "mkdir"  // directory created at Dst
	ExecKindRmdir  = "rmdir"  // source directory Src removed after it became empty
)

// ExecOp is one journaled filesystem step of an executing run.
type ExecOp struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	RunID      uint      `gorm:"index" json:"run_id"`
	SessionID  uint      `gorm:"index:idx_execop_session_state,priority:1" json:"session_id"`
	EntryID    uint      `gorm:"index" json:"entry_id"` // FileEntry whose decision produced the op (0 for none)
	Kind       string    `gorm:"size:16" json:"kind"`
	Src        string    `gorm:"size:1024" json:"src"`
	Dst        string    `gorm:"size:1024" json:"dst"`
	IsDir      bool      `json:"is_dir"`
	Size       int64     `json:"size"`     // source size when the op started
	ModTime    time.Time `json:"mod_time"` // source mtime when the op started
	DstSize    int64     `json:"dst_size"` // dst stat after verification (revert checks it is unchanged)
	DstModTime time.Time `json:"dst_mod_time"`
	State      string    `gorm:"size:16;index:idx_execop_session_state,priority:2" json:"state"`
	Error      string    `gorm:"type:text" json:"error"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (r *Repository) CreateExecOp(op *ExecOp) error {
	return WithRetry(func() error { return r.db.Create(op).Error })
}

// SetExecOpState advances an op; errMsg is stored as given (empty clears it).
func (r *Repository) SetExecOpState(op *ExecOp, state, errMsg string) error {
	op.State, op.Error = state, errMsg
	return WithRetry(func() error {
		return r.db.Model(&ExecOp{}).Where("id = ?", op.ID).
			Updates(map[string]any{"state": state, "error": errMsg, "updated_at": time.Now()}).Error
	})
}

// SetExecOpDstStat records the verified destination stat.
func (r *Repository) SetExecOpDstStat(op *ExecOp, size int64, mtime time.Time) error {
	op.DstSize, op.DstModTime = size, mtime
	return WithRetry(func() error {
		return r.db.Model(&ExecOp{}).Where("id = ?", op.ID).
			Updates(map[string]any{"dst_size": size, "dst_mod_time": mtime}).Error
	})
}

// ListExecOps returns a run's ops in execution order.
func (r *Repository) ListExecOps(runID uint) ([]ExecOp, error) {
	var ops []ExecOp
	err := r.db.Where("run_id = ?", runID).Order("id ASC").Find(&ops).Error
	return ops, err
}

// ListUnfinishedExecOps returns the session's ops a crash or stop left in an
// intermediate state, oldest first.
func (r *Repository) ListUnfinishedExecOps(sessionID uint) ([]ExecOp, error) {
	var ops []ExecOp
	err := r.db.Where("session_id = ? AND state IN ?", sessionID, []string{ExecPending, ExecCopied, ExecVerified}).
		Order("id ASC").Find(&ops).Error
	return ops, err
}

// FindDoneExecOp returns the newest done op of the session that wrote dst, or
// nil. The executor uses it to tell its own earlier output from a foreign
// file when resuming a partially executed directory.
func (r *Repository) FindDoneExecOp(sessionID uint, dst string) (*ExecOp, error) {
	var ops []ExecOp
	err := r.db.Where("session_id = ? AND dst = ? AND state = ?", sessionID, dst, ExecDone).
		Order("id DESC").Limit(1).Find(&ops).Error
	if err != nil || len(ops) == 0 {
		return nil, err
	}
	return &ops[0], nil
}

// LatestExecOpByEntry returns the newest op per entry id.
func (r *Repository) LatestExecOpByEntry(entryIDs []uint) (map[uint]ExecOp, error) {
	out := map[uint]ExecOp{}
	if len(entryIDs) == 0 {
		return out, nil
	}
	var ops []ExecOp
	if err := r.db.Where("entry_id IN ?", entryIDs).Order("id ASC").Find(&ops).Error; err != nil {
		return nil, err
	}
	for _, op := range ops {
		out[op.EntryID] = op
	}
	return out, nil
}

// MarkEntryExecuted flags a decided entry as carried out (column-scoped).
func (r *Repository) MarkEntryExecuted(id uint, mode string) error {
	now := time.Now()
	return WithRetry(func() error {
		return r.db.Model(&FileEntry{}).Where("id = ?", id).
			Updates(map[string]any{"executed": true, "executed_at": &now, "execute_mode": mode}).Error
	})
}

// UnmarkEntryExecuted clears the executed flag after a revert.
func (r *Repository) UnmarkEntryExecuted(id uint) error {
	return WithRetry(func() error {
		return r.db.Model(&FileEntry{}).Where("id = ?", id).
			Updates(map[string]any{"executed": false, "executed_at": nil, "execute_mode": ""}).Error
	})
}

// ListPlannedEntries returns the session's decided, not yet executed entries
// in deterministic order (original path, then id).
func (r *Repository) ListPlannedEntries(sessionID uint) ([]FileEntry, error) {
	var files []FileEntry
	err := r.db.Where("scan_session_id = ? AND operation != '' AND new_path != '' AND executed = ?", sessionID, false).
		Order("original_path ASC, id ASC").Find(&files).Error
	return files, err
}

// ListDecidedEntries returns every entry of the session that carries a
// target, executed or not — the set ancestors are resolved against.
func (r *Repository) ListDecidedEntries(sessionID uint) ([]FileEntry, error) {
	var files []FileEntry
	err := r.db.Where("scan_session_id = ? AND operation != '' AND new_path != ''", sessionID).
		Order("original_path ASC, id ASC").Find(&files).Error
	return files, err
}

// SearchEntries finds entries by exact original path, path substring or name
// substring (case-insensitive name match), at most limit rows.
func (r *Repository) SearchEntries(sessionID uint, q string, limit int) ([]FileEntry, error) {
	var files []FileEntry
	q = strings.TrimPrefix(strings.TrimSpace(q), "/")
	pattern := "%" + likeLiteral(q) + "%"
	tx := r.db.Where("scan_session_id = ?", sessionID)
	err := tx.Where(r.db.Where("original_path = ?", q).
		Or(ciLikeCondition(r.db, "original_path"), pattern).
		Or(nameSearchCondition(r.db), pattern)).
		Order("depth ASC, original_path ASC").Limit(limit).Find(&files).Error
	return files, err
}

// CollisionKey normalizes a path for collision detection: slash-cleaned,
// without leading slash, Unicode NFC and lower-cased. Two targets with the
// same key collide on case-insensitive or normalization-insensitive
// filesystems (SMB, macOS) even if their bytes differ.
func CollisionKey(p string) string {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	return strings.ToLower(norm.NFC.String(p))
}

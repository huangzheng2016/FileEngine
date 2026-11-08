package db

import (
	"FileEngine/internal/pathutil"
	"errors"
	"fmt"
	"path"
	"strings"

	"gorm.io/gorm"
)

// ReviewConfidence is the confidence below which a decision needs user review.
const ReviewConfidence = 0.6

// Decision is one requested decision for an entry.
type Decision struct {
	Path       string // original path (leading "/" tolerated)
	Role       string // unit / container / file / skip / "" (undecide)
	NewPath    string // target of a unit or file
	Reason     string
	Confidence float64 // 0 = not given (treated as confident)
	Override   bool    // a unit/skip directory may override decided descendants
}

// DecideResult reports what a decision stored.
type DecideResult struct {
	Path            string   `json:"path"`
	Role            string   `json:"role"`
	NewPath         string   `json:"new_path,omitempty"`
	Renamed         bool     `json:"renamed,omitempty"`
	NeedsReview     bool     `json:"needs_review,omitempty"`
	Members         int64    `json:"members,omitempty"`
	Overridden      []string `json:"overridden,omitempty"`
	OverriddenCount int64    `json:"overridden_count,omitempty"`
}

// Rejection is a decision refused for a reason the caller can fix; tools
// report it as a success:false result instead of failing the run.
type Rejection struct{ Msg string }

func (e *Rejection) Error() string { return e.Msg }

func reject(format string, args ...any) error { return &Rejection{Msg: fmt.Sprintf(format, args...)} }

// IsRejection reports whether err is a fixable decision rejection.
func IsRejection(err error) bool {
	var r *Rejection
	return errors.As(err, &r)
}

// ValidateTargetPath checks that a target or category path is a legal,
// root-confined slash path: no NUL, no ".." segment, not the bare root.
// "" is allowed (it clears a target).
func ValidateTargetPath(p string) error { return pathutil.ValidateFSPath(p) }

// decidedRoles are roles that carry an effective decision of their own.
var decidedRoles = []string{RoleUnit, RoleFile, RoleSkip}

// Decide applies one decision under the one-effective-decision invariant:
//   - nothing may be decided below a unit (or a skipped directory): rejected
//     with guidance to split_unit first;
//   - a unit/skip directory over decided descendants needs Override and
//     reports what it overrode; its descendants become members;
//   - unit/file targets get plan-time collision naming.
func (r *Repository) Decide(sessionID uint, d Decision) (*DecideResult, error) {
	defer r.touchCounts(sessionID)
	var res *DecideResult
	err := WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			var err error
			res, err = decideTx(tx, sessionID, d)
			return err
		})
	})
	return res, err
}

// DecideOutcome is one item of DecideBatch.
type DecideOutcome struct {
	Result *DecideResult
	Err    error // a Rejection, or nil
}

// DecideBatch applies decisions in chunked transactions; rejections are
// reported per item, storage failures abort.
func (r *Repository) DecideBatch(sessionID uint, ds []Decision) ([]DecideOutcome, error) {
	defer r.touchCounts(sessionID)
	out := make([]DecideOutcome, len(ds))
	const chunk = 200
	for start := 0; start < len(ds); start += chunk {
		end := min(start+chunk, len(ds))
		err := WithRetry(func() error {
			return r.db.Transaction(func(tx *gorm.DB) error {
				for i := start; i < end; i++ {
					res, err := decideTx(tx, sessionID, ds[i])
					if err != nil && !IsRejection(err) {
						return err
					}
					out[i] = DecideOutcome{Result: res, Err: err}
				}
				return nil
			})
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func decideTx(tx *gorm.DB, sessionID uint, d Decision) (*DecideResult, error) {
	p := strings.Trim(d.Path, "/")
	var e FileEntry
	if err := tx.Where("scan_session_id = ? AND original_path = ?", sessionID, p).Take(&e).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, reject("条目不存在：%s", d.Path)
		}
		return nil, err
	}
	if owner, err := governingAncestor(tx, sessionID, p); err != nil {
		return nil, err
	} else if owner != nil {
		return nil, underUnitRejection(owner)
	}

	isDir := e.FileType == "directory"
	role := d.Role
	switch {
	case role == RoleFile && isDir:
		role = RoleUnit
	case role == RoleUnit && !isDir:
		role = RoleFile
	}
	switch role {
	case RoleUnit, RoleFile, RoleContainer, RoleSkip, RoleUndecided:
	default:
		return nil, reject("未知角色 %q（可用 unit / container / file / skip）", d.Role)
	}
	if role == RoleContainer && !isDir {
		return nil, reject("%s 是文件，不能作为 container", p)
	}

	target := ""
	if role == RoleUnit || role == RoleFile {
		if err := ValidateTargetPath(d.NewPath); err != nil {
			return nil, reject("invalid new_path: %v", err)
		}
		target = NormalizeTargetPath(d.NewPath)
		if target == "" {
			return nil, reject("%s 需要目标路径 new_path", p)
		}
	}

	res := &DecideResult{Path: p, Role: role}
	ownsSubtree := isDir && (role == RoleUnit || role == RoleSkip)
	if ownsSubtree {
		decided, count, err := decidedDescendants(tx, sessionID, p, 10)
		if err != nil {
			return nil, err
		}
		if count > 0 && !d.Override {
			return nil, reject("「%s」下已有 %d 个条目做了单独决定（如 %s）。确认要整体处理并覆盖这些决定，请带 override:true 重试；否则改为 container 并逐个处理子项",
				p, count, strings.Join(decided, "、"))
		}
		res.Overridden, res.OverriddenCount = decided, count
	}

	if target != "" {
		final, err := uniqueTarget(tx, sessionID, p, target)
		if err != nil {
			return nil, err
		}
		res.NewPath, res.Renamed = final, final != target
	}
	op := ""
	if res.NewPath != "" {
		op = "planned"
	}
	res.NeedsReview = res.NewPath != "" && d.Confidence > 0 && d.Confidence < ReviewConfidence
	if err := tx.Model(&FileEntry{}).Where("id = ?", e.ID).Updates(map[string]any{
		"role": role, "new_path": res.NewPath, "new_path_key": targetKey(res.NewPath), "operation": op, "reason": d.Reason,
		"tagged": role != RoleUndecided, "needs_review": res.NeedsReview, "confidence": d.Confidence,
	}).Error; err != nil {
		return nil, err
	}

	if isDir {
		under := entriesUnder(tx, sessionID, p)
		switch {
		case ownsSubtree:
			q := under.Updates(map[string]any{"role": RoleMember, "new_path": "", "new_path_key": "", "operation": "", "reason": MemberReason(p, role),
				"tagged": true, "needs_review": false, "confidence": 0})
			if q.Error != nil {
				return nil, q.Error
			}
			res.Members = q.RowsAffected
		case e.Role == RoleUnit || e.Role == RoleSkip:
			// The directory stopped owning its subtree: members become undecided.
			if err := under.Where("role = ?", RoleMember).
				Updates(map[string]any{"role": RoleUndecided, "tagged": false}).Error; err != nil {
				return nil, err
			}
		}
	}
	return res, nil
}

// MemberReason is the reason stored on members of a unit or skipped directory.
func MemberReason(owner, ownerRole string) string {
	if ownerRole == RoleSkip {
		return "随「" + owner + "」整体保持原位"
	}
	return "随「" + owner + "」整体移动"
}

// governingAncestor returns the nearest strict ancestor that owns its
// subtree (a unit or a skipped directory), or nil.
func governingAncestor(tx *gorm.DB, sessionID uint, p string) (*FileEntry, error) {
	anc := pathAncestors(path.Dir(p))
	if len(anc) == 0 {
		return nil, nil
	}
	var rows []FileEntry
	if err := tx.Where("scan_session_id = ? AND original_path IN ? AND role IN ?", sessionID, anc, []string{RoleUnit, RoleSkip}).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	var best *FileEntry
	for i := range rows {
		if best == nil || len(rows[i].OriginalPath) > len(best.OriginalPath) {
			best = &rows[i]
		}
	}
	return best, nil
}

func underUnitRejection(owner *FileEntry) error {
	if owner.Role == RoleSkip {
		return reject("该目录已整体保持原位「%s」；如需拆分请先调用 split_unit", owner.OriginalPath)
	}
	return reject("该目录已作为整体「%s」移动到 %s；如需拆分请先调用 split_unit", owner.OriginalPath, owner.NewPath)
}

// decidedDescendants lists up to limit descendants with a decision of their
// own and counts them all.
func decidedDescendants(tx *gorm.DB, sessionID uint, p string, limit int) ([]string, int64, error) {
	q := entriesUnder(tx, sessionID, p).Where("(role IN ? OR operation != '')", decidedRoles)
	var n int64
	if err := q.Count(&n).Error; err != nil || n == 0 {
		return nil, n, err
	}
	var paths []string
	err := entriesUnder(tx, sessionID, p).Where("(role IN ? OR operation != '')", decidedRoles).Order("original_path").Limit(limit).Pluck("original_path", &paths).Error
	return paths, n, err
}

// uniqueTarget returns target or its first "name (n).ext" variant that no
// other entry of the session targets.
func uniqueTarget(tx *gorm.DB, sessionID uint, originalPath, target string) (string, error) {
	for n := 1; n <= maxCollisionVariants; n++ {
		candidate := target
		if n > 1 {
			candidate = CollisionName(target, n)
		}
		taken, err := targetTaken(tx, sessionID, candidate, originalPath)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free target name for %s after %d variants", target, maxCollisionVariants)
}

// SplitUnit turns a unit (or skipped directory) back into a container: its
// target is cleared and its members become undecided. Returns how many
// members were released.
func (r *Repository) SplitUnit(sessionID uint, p string) (int64, error) {
	defer r.touchCounts(sessionID)
	p = strings.Trim(p, "/")
	var released int64
	err := WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			var e FileEntry
			if err := tx.Where("scan_session_id = ? AND original_path = ?", sessionID, p).Take(&e).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return reject("条目不存在：%s", p)
				}
				return err
			}
			if e.FileType != "directory" || (e.Role != RoleUnit && e.Role != RoleSkip) {
				return reject("%s 不是整体移动或整体跳过的目录，无需拆分", p)
			}
			if err := tx.Model(&FileEntry{}).Where("id = ?", e.ID).Updates(map[string]any{
				"role": RoleContainer, "new_path": "", "new_path_key": "", "operation": "", "reason": "拆分为逐项处理", "needs_review": false,
			}).Error; err != nil {
				return err
			}
			q := entriesUnder(tx, sessionID, p).Where("role = ?", RoleMember).
				Updates(map[string]any{"role": RoleUndecided, "tagged": false})
			released = q.RowsAffected
			return q.Error
		})
	})
	return released, err
}

// SetTargetUnique decides an entry as file (or unit for a directory) with the
// given target, overriding decided descendants; "" undecides it. Returns the
// stored target and how many decided descendants were overridden.
func (r *Repository) SetTargetUnique(sessionID uint, originalPath, newPath, fileType string) (string, int64, error) {
	role := RoleFile
	if fileType == "directory" {
		role = RoleUnit
	}
	if newPath == "" {
		role = RoleUndecided
	}
	res, err := r.Decide(sessionID, Decision{Path: originalPath, Role: role, NewPath: newPath, Override: true})
	if err != nil {
		return "", 0, err
	}
	return res.NewPath, res.OverriddenCount, nil
}

package db

import (
	"fmt"
	"path"
	"strings"

	"gorm.io/gorm"
)

// NormalizeTargetPath returns the stored form of a planned target: cleaned
// and "/"-rooted ("" stays "" — it means "no target"). Every writer of
// FileEntry.NewPath goes through it so prefix queries (preview, category
// usage) see one spelling.
func NormalizeTargetPath(p string) string {
	if p == "" {
		return ""
	}
	return path.Clean("/" + p)
}

// CollisionName returns the n-th (n >= 2) alternative of a target path:
// "/a/b.txt" → "/a/b (2).txt"; ".tar.gz" and dot-files keep their suffix
// intact ("x.tar (2).gz" would be wrong, ".bashrc" has no extension).
func CollisionName(p string, n int) string {
	dir, base := path.Split(p)
	ext := ""
	lower := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") && len(base) > len(".tar.gz"):
		ext = base[len(base)-len(".tar.gz"):]
	case strings.HasPrefix(base, ".") && strings.Count(base, ".") == 1:
		ext = ""
	default:
		ext = path.Ext(base)
	}
	stem := strings.TrimSuffix(base, ext)
	return fmt.Sprintf("%s%s (%d)%s", dir, stem, n, ext)
}

// maxCollisionVariants bounds the "name (n)" search.
const maxCollisionVariants = 1000

// targetKey is the stored new_path_key of a target ("" for no target).
func targetKey(newPath string) string {
	if newPath == "" {
		return ""
	}
	return CollisionKey(newPath)
}

// targetTaken reports whether another entry of the session targets a path
// with the same CollisionKey as candidate (indexed new_path_key lookup).
func targetTaken(tx *gorm.DB, sessionID uint, candidate, originalPath string) (bool, error) {
	var ids []uint
	err := tx.Model(&FileEntry{}).
		Where("scan_session_id = ? AND new_path_key = ? AND original_path <> ?", sessionID, CollisionKey(candidate), originalPath).
		Limit(1).Pluck("id", &ids).Error
	return len(ids) > 0, err
}

// ResetPlans clears the targets of the given entries of a session and marks
// them undecided so the next tagging/chat run redoes them; a reset unit
// releases its members. Returns the
// number of entries reset.
func (r *Repository) ResetPlans(sessionID uint, ids []uint) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	defer r.touchCounts(sessionID)
	var n int64
	err := WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			var entries []FileEntry
			if err := tx.Select("id", "original_path", "file_type").
				Where("scan_session_id = ? AND id IN ?", sessionID, ids).Find(&entries).Error; err != nil {
				return err
			}
			if len(entries) == 0 {
				n = 0
				return nil
			}
			found := make([]uint, len(entries))
			for i, e := range entries {
				found[i] = e.ID
			}
			if err := tx.Model(&FileEntry{}).Where("id IN ?", found).
				Updates(map[string]any{"role": RoleUndecided, "reason": "", "new_path": "", "new_path_key": "", "operation": "", "tagged": false, "needs_review": false, "confidence": 0}).Error; err != nil {
				return err
			}
			for _, e := range entries {
				if e.FileType != "directory" {
					continue
				}
				if err := entriesUnder(tx, sessionID, e.OriginalPath).Where("role = ?", RoleMember).
					Updates(map[string]any{"role": RoleUndecided, "reason": "", "tagged": false}).Error; err != nil {
					return err
				}
			}
			n = int64(len(entries))
			return nil
		})
	})
	return n, err
}

// UpdateFileFields writes the given columns of one entry (column-scoped —
// never a full-row Save of a stale struct).
func (r *Repository) UpdateFileFields(id uint, fields map[string]any) error {
	return r.db.Model(&FileEntry{}).Where("id = ?", id).Updates(fields).Error
}

// ConfirmReview clears needs_review on the given entries of a session (the
// user accepts the low-confidence decisions). Returns how many changed.
func (r *Repository) ConfirmReview(sessionID uint, ids []uint) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := r.db.Model(&FileEntry{}).Where("scan_session_id = ? AND id IN ? AND needs_review = ?", sessionID, ids, true).
		Update("needs_review", false)
	return res.RowsAffected, res.Error
}

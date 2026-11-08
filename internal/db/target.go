package db

import (
	"fmt"
	"path"
	"strings"

	"gorm.io/gorm"
)

// Every writer of FileEntry.NewPath goes through it so prefix queries see one
// spelling.
func NormalizeTargetPath(p string) string {
	if p == "" {
		return ""
	}
	return path.Clean("/" + p)
}

// .tar.gz and dot-files keep their suffix intact.
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

const maxCollisionVariants = 1000

func targetKey(newPath string) string {
	if newPath == "" {
		return ""
	}
	return CollisionKey(newPath)
}

func targetTaken(tx *gorm.DB, sessionID uint, candidate, originalPath string) (bool, error) {
	var ids []uint
	err := tx.Model(&FileEntry{}).
		Where("scan_session_id = ? AND new_path_key = ? AND original_path <> ?", sessionID, CollisionKey(candidate), originalPath).
		Limit(1).Pluck("id", &ids).Error
	return len(ids) > 0, err
}

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

func (r *Repository) UpdateFileFields(id uint, fields map[string]any) error {
	return r.db.Model(&FileEntry{}).Where("id = ?", id).Updates(fields).Error
}

func (r *Repository) ConfirmReview(sessionID uint, ids []uint) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := r.db.Model(&FileEntry{}).Where("scan_session_id = ? AND id IN ? AND needs_review = ?", sessionID, ids, true).
		Update("needs_review", false)
	return res.RowsAffected, res.Error
}

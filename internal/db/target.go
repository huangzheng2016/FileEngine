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

// SetTargetUnique plans an entry's target, resolving collisions at plan time:
// when another entry of the session already targets newPath, the first free
// "name (n).ext" variant is used instead. For a directory with a target, the
// planned descendants are cleared (outer target overrides inner). Returns the
// final target and how many children were cleared. newPath "" clears.
func (r *Repository) SetTargetUnique(sessionID uint, originalPath, newPath, fileType string) (string, int64, error) {
	newPath = NormalizeTargetPath(newPath)
	op := ""
	if newPath != "" {
		op = "planned"
	}
	final := newPath
	var cleared int64
	err := WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			final = newPath
			if newPath != "" {
				free := false
				for n := 1; n <= maxCollisionVariants; n++ {
					candidate := newPath
					if n > 1 {
						candidate = CollisionName(newPath, n)
					}
					var taken int64
					if err := tx.Model(&FileEntry{}).
						Where("scan_session_id = ? AND new_path = ? AND original_path <> ?", sessionID, candidate, originalPath).
						Count(&taken).Error; err != nil {
						return err
					}
					if taken == 0 {
						final, free = candidate, true
						break
					}
				}
				if !free {
					return fmt.Errorf("no free target name for %s after %d variants", newPath, maxCollisionVariants)
				}
			}
			if err := tx.Model(&FileEntry{}).
				Where("scan_session_id = ? AND original_path = ?", sessionID, originalPath).
				Updates(map[string]any{"new_path": final, "operation": op}).Error; err != nil {
				return err
			}
			cleared = 0
			if fileType == "directory" && final != "" {
				res := tx.Model(&FileEntry{}).
					Where("scan_session_id = ? AND original_path LIKE ?"+likeEscapeClause+" AND operation != ''", sessionID, childPathPattern(originalPath)).
					Updates(map[string]any{"new_path": "", "operation": ""})
				if res.Error != nil {
					return res.Error
				}
				cleared = res.RowsAffected
			}
			return nil
		})
	})
	return final, cleared, err
}

// ResetPlans clears the targets of the given entries of a session and marks
// them untagged so the next tagging/chat run redoes them; a reset directory
// also untags its descendants (they were cascade-tagged with it). Returns the
// number of entries reset.
func (r *Repository) ResetPlans(sessionID uint, ids []uint) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
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
				Updates(map[string]any{"new_path": "", "operation": "", "tagged": false}).Error; err != nil {
				return err
			}
			for _, e := range entries {
				if e.FileType != "directory" {
					continue
				}
				if err := tx.Model(&FileEntry{}).
					Where("scan_session_id = ? AND original_path LIKE ?"+likeEscapeClause, sessionID, childPathPattern(e.OriginalPath)).
					Update("tagged", false).Error; err != nil {
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

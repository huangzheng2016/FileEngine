package db

import (
	"time"

	"gorm.io/gorm"
)

// Scanner-side repository methods: streaming bulk load, incremental rescan
// diff, metadata/duplicate passes.

// Reader returns the read-only pool when this repository wraps the global DB
// (SQLite reader pool, see ReadDB) and the repository's own handle otherwise
// (tests with private databases).
func (r *Repository) Reader() *gorm.DB {
	if r.db == DB && readDB != nil {
		return readDB
	}
	return r.db
}

// maxBindVars bounds the placeholders of one multi-row INSERT. The pure-Go
// SQLite driver binds in O(params^2) per statement, so a few dozen rows per
// statement is fastest there (900-row statements were 8x slower than 40-row
// ones for 50k entries); MySQL and PostgreSQL allow 65535 placeholders.
func maxBindVars(db *gorm.DB) int {
	if db.Dialector.Name() == "sqlite" {
		return 1500
	}
	return 30000
}

// InsertBatchRows is the multi-row INSERT size for FileEntry: as many rows as
// fit under the bind-variable budget given the current column count.
func (r *Repository) InsertBatchRows() int {
	stmt := &gorm.Statement{DB: r.db}
	if err := stmt.Parse(&FileEntry{}); err != nil || len(stmt.Schema.DBNames) == 0 {
		return 40
	}
	return max(1, maxBindVars(r.db)/len(stmt.Schema.DBNames))
}

// EntryUpdate is one column-scoped FileEntry update by primary key.
type EntryUpdate struct {
	ID   uint
	Cols map[string]any
}

// ApplyScanBatch commits inserts and column updates in one transaction.
func (r *Repository) ApplyScanBatch(inserts []FileEntry, updates []EntryUpdate) error {
	if len(inserts) == 0 && len(updates) == 0 {
		return nil
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		if len(inserts) > 0 {
			if err := tx.CreateInBatches(inserts, r.InsertBatchRows()).Error; err != nil {
				return err
			}
		}
		for _, u := range updates {
			if err := tx.Model(&FileEntry{}).Where("id = ?", u.ID).Updates(u.Cols).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ScanIndexRow is the subset of an existing entry the rescan diff needs.
type ScanIndexRow struct {
	ID           uint
	OriginalPath string
	Size         int64
	ModTime      time.Time
	FileType     string
	ChildCount   int
	SubdirCount  int
}

// ForEachScanIndexRow pages through a session's entries by id (reader pool).
func (r *Repository) ForEachScanIndexRow(sessionID uint, fn func(ScanIndexRow)) error {
	const page = 20000
	var cursor uint
	for {
		var rows []ScanIndexRow
		err := r.Reader().Model(&FileEntry{}).
			Select("id, original_path, size, mod_time, file_type, child_count, subdir_count").
			Where("scan_session_id = ? AND id > ?", sessionID, cursor).
			Order("id").Limit(page).Scan(&rows).Error
		if err != nil {
			return err
		}
		for _, row := range rows {
			fn(row)
		}
		if len(rows) < page {
			return nil
		}
		cursor = rows[len(rows)-1].ID
	}
}

// DeleteEntriesByIDs deletes entries in chunks of sorted ids, one transaction
// per chunk so a huge removal never holds the writer for long.
func (r *Repository) DeleteEntriesByIDs(ids []uint) error {
	const chunk = 1000
	for i := 0; i < len(ids); i += chunk {
		end := min(i+chunk, len(ids))
		part := ids[i:end]
		if err := WithRetry(func() error {
			return r.db.Where("id IN ?", part).Delete(&FileEntry{}).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

// AdoptUnitMembers turns undecided entries under a unit directory into its
// members (one effective decision per path). Used after a rescan inserted or
// reset rows inside units.
func (r *Repository) AdoptUnitMembers(sessionID uint) (int64, error) {
	defer r.touchCounts(sessionID)
	var units []string
	if err := r.Reader().Model(&FileEntry{}).
		Where("scan_session_id = ? AND role = ?", sessionID, RoleUnit).
		Pluck("original_path", &units).Error; err != nil {
		return 0, err
	}
	var total int64
	for _, u := range units {
		var n int64
		err := WithRetry(func() error {
			res := entriesUnder(r.db, sessionID, u).Where("role = ?", RoleUndecided).
				Updates(map[string]any{"role": RoleMember, "tagged": true})
			n = res.RowsAffected
			return res.Error
		})
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// MediaCandidate is a file row the metadata pass may inspect.
type MediaCandidate struct {
	ID           uint
	OriginalPath string
	Name         string
	Size         int64
	ModTime      time.Time
}

// MediaCandidatesAfter pages files whose metadata was never extracted
// (date_source = "") by id cursor (reader pool).
func (r *Repository) MediaCandidatesAfter(sessionID, cursor uint, limit int) ([]MediaCandidate, error) {
	var rows []MediaCandidate
	err := r.Reader().Model(&FileEntry{}).
		Select("id, original_path, name, size, mod_time").
		Where("scan_session_id = ? AND file_type = ? AND date_source = ? AND id > ?", sessionID, "file", "", cursor).
		Order("id").Limit(limit).Scan(&rows).Error
	return rows, err
}

// DupCandidate is one file in a same-size bucket.
type DupCandidate struct {
	ID           uint
	OriginalPath string
	Size         int64
	ModTime      time.Time
	Hash         string
	Role         string
}

// DuplicateSizeBuckets returns every file size (> 0) shared by two or more
// files of the session.
func (r *Repository) DuplicateSizeBuckets(sessionID uint) ([]int64, error) {
	var sizes []int64
	err := r.Reader().Model(&FileEntry{}).
		Where("scan_session_id = ? AND file_type = ? AND size > 0", sessionID, "file").
		Group("size").Having("COUNT(*) > 1").Order("size").
		Pluck("size", &sizes).Error
	return sizes, err
}

// FilesOfSize lists a session's files with exactly this size.
func (r *Repository) FilesOfSize(sessionID uint, size int64) ([]DupCandidate, error) {
	var rows []DupCandidate
	err := r.Reader().Model(&FileEntry{}).
		Select("id, original_path, size, mod_time, hash, role").
		Where("scan_session_id = ? AND file_type = ? AND size = ?", sessionID, "file", size).
		Order("id").Scan(&rows).Error
	return rows, err
}

// ResetDuplicates clears every duplicate link of a session (duplicate roles
// go back to undecided) before the duplicate pass recomputes them. With
// clearHashes the stored hashes go too (detection switched off).
func (r *Repository) ResetDuplicates(sessionID uint, clearHashes bool) error {
	return WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&FileEntry{}).
				Where("scan_session_id = ? AND role = ?", sessionID, RoleDuplicate).
				Update("role", RoleUndecided).Error; err != nil {
				return err
			}
			cols := map[string]any{"duplicate_of": nil}
			q := tx.Model(&FileEntry{}).Where("scan_session_id = ? AND duplicate_of IS NOT NULL", sessionID)
			if clearHashes {
				cols["hash"] = ""
				q = tx.Model(&FileEntry{}).Where("scan_session_id = ? AND (duplicate_of IS NOT NULL OR hash <> ?)", sessionID, "")
			}
			return q.Updates(cols).Error
		})
	})
}

// Optimize refreshes SQLite planner statistics after a bulk load; a no-op on
// other dialects.
func (r *Repository) Optimize() error {
	if r.db.Dialector.Name() != "sqlite" {
		return nil
	}
	return r.db.Exec("PRAGMA optimize").Error
}

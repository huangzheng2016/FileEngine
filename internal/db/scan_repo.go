package db

import (
	"time"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"

	"gorm.io/gorm"
)

// The SQLite reader pool when this repository wraps the global DB; tests
// with private databases use their own handle.
func (r *Repository) Reader() *gorm.DB {
	if r.db == dal.DB() {
		return dal.ReadDB()
	}
	return r.db
}

// The pure-Go SQLite driver binds in O(params^2) per statement, so a few
// dozen rows per statement is fastest there; MySQL and PostgreSQL allow 65535
// placeholders.
func maxBindVars(db *gorm.DB) int {
	if db.Dialector.Name() == "sqlite" {
		return 1500
	}
	return 30000
}

func (r *Repository) InsertBatchRows() int {
	stmt := &gorm.Statement{DB: r.db}
	if err := stmt.Parse(&model.FileEntry{}); err != nil || len(stmt.Schema.DBNames) == 0 {
		return 40
	}
	return max(1, maxBindVars(r.db)/len(stmt.Schema.DBNames))
}

type EntryUpdate struct {
	ID   uint
	Cols map[string]any
}

func (r *Repository) ApplyScanBatch(inserts []model.FileEntry, updates []EntryUpdate) error {
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
			if err := tx.Model(&model.FileEntry{}).Where("id = ?", u.ID).Updates(u.Cols).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

type ScanIndexRow struct {
	ID           uint
	OriginalPath string
	Size         int64
	ModTime      time.Time
	FileType     string
	ChildCount   int
	SubdirCount  int
}

func (r *Repository) ForEachScanIndexRow(sessionID uint, fn func(ScanIndexRow)) error {
	const page = 20000
	var cursor uint
	for {
		var rows []ScanIndexRow
		err := r.Reader().Model(&model.FileEntry{}).
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

// One transaction per chunk so a huge removal never holds the writer for
// long.
func (r *Repository) DeleteEntriesByIDs(ids []uint) error {
	const chunk = 1000
	for i := 0; i < len(ids); i += chunk {
		end := min(i+chunk, len(ids))
		part := ids[i:end]
		if err := dal.WithRetry(func() error {
			return r.db.Where("id IN ?", part).Delete(&model.FileEntry{}).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

// Used after a rescan inserted or reset rows inside units.
func (r *Repository) AdoptUnitMembers(sessionID uint) (int64, error) {
	defer r.touchCounts(sessionID)
	var units []string
	if err := r.Reader().Model(&model.FileEntry{}).
		Where("scan_session_id = ? AND role = ?", sessionID, RoleUnit).
		Pluck("original_path", &units).Error; err != nil {
		return 0, err
	}
	var total int64
	for _, u := range units {
		var n int64
		err := dal.WithRetry(func() error {
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

type MediaCandidate struct {
	ID           uint
	OriginalPath string
	Name         string
	Size         int64
	ModTime      time.Time
}

// CountMediaCandidates counts the rows MediaCandidatesAfter will page through.
func (r *Repository) CountMediaCandidates(sessionID uint) (int64, error) {
	var n int64
	err := r.Reader().Model(&model.FileEntry{}).
		Where("scan_session_id = ? AND file_type = ? AND date_source = ?", sessionID, "file", "").
		Count(&n).Error
	return n, err
}

func (r *Repository) MediaCandidatesAfter(sessionID, cursor uint, limit int) ([]MediaCandidate, error) {
	var rows []MediaCandidate
	err := r.Reader().Model(&model.FileEntry{}).
		Select("id, original_path, name, size, mod_time").
		Where("scan_session_id = ? AND file_type = ? AND date_source = ? AND id > ?", sessionID, "file", "", cursor).
		Order("id").Limit(limit).Scan(&rows).Error
	return rows, err
}

type DupCandidate struct {
	ID           uint
	OriginalPath string
	Size         int64
	ModTime      time.Time
	Hash         string
	Role         string
}

// sharedSize keeps files whose size another file of the session shares; the
// probe and the outer scan both run on idx_session_type_size.
const sharedSize = "EXISTS (SELECT 1 FROM file_entries f2 WHERE f2.scan_session_id = file_entries.scan_session_id" +
	" AND f2.file_type = file_entries.file_type AND f2.size = file_entries.size AND f2.id <> file_entries.id)"

// DuplicateCandidateBytes is the total size of the files that share their
// size with another file: the most the duplicate pass can read in full.
func (r *Repository) DuplicateCandidateBytes(sessionID uint) (int64, error) {
	var total int64
	err := r.Reader().Model(&model.FileEntry{}).
		Select("COALESCE(SUM(size), 0)").
		Where("scan_session_id = ? AND file_type = ? AND size > 0", sessionID, "file").
		Where(sharedSize).Scan(&total).Error
	return total, err
}

// FilesBySizeAfter pages the files that share their size with another file in
// (size, id) order, after the cursor (afterSize, afterID); (0, 0) starts.
func (r *Repository) FilesBySizeAfter(sessionID uint, afterSize int64, afterID uint, limit int) ([]DupCandidate, error) {
	var rows []DupCandidate
	err := filesBySize(r.Reader(), sessionID, afterSize, afterID, limit).Scan(&rows).Error
	return rows, err
}

// Empty files are excluded through the cursor itself, so the index range has
// a single lower bound and starts at the cursor.
func filesBySize(tx *gorm.DB, sessionID uint, afterSize int64, afterID uint, limit int) *gorm.DB {
	afterSize = max(afterSize, 1)
	return tx.Model(&model.FileEntry{}).
		Select("id, original_path, size, mod_time, hash, role").
		Where("scan_session_id = ? AND file_type = ?", sessionID, "file").
		Where("size >= ? AND (size > ? OR id > ?)", afterSize, afterSize, afterID).
		Where(sharedSize).
		Order("size, id").Limit(limit)
}

func (r *Repository) ResetDuplicates(sessionID uint, clearHashes bool) error {
	return dal.WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model.FileEntry{}).
				Where("scan_session_id = ? AND role = ?", sessionID, RoleDuplicate).
				Update("role", RoleUndecided).Error; err != nil {
				return err
			}
			cols := map[string]any{"duplicate_of": nil}
			q := tx.Model(&model.FileEntry{}).Where("scan_session_id = ? AND duplicate_of IS NOT NULL", sessionID)
			if clearHashes {
				cols["hash"] = ""
				q = tx.Model(&model.FileEntry{}).Where("scan_session_id = ? AND (duplicate_of IS NOT NULL OR hash <> ?)", sessionID, "")
			}
			return q.Updates(cols).Error
		})
	})
}

func (r *Repository) Optimize() error {
	if r.db.Dialector.Name() != "sqlite" {
		return nil
	}
	return r.db.Exec("PRAGMA optimize").Error
}

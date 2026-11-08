package db

import (
	"errors"
	"math/rand/v2"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) DB() *gorm.DB {
	return r.db
}

// ============ ScanSession ============

func (r *Repository) CreateSession(s *ScanSession) error {
	return r.db.Create(s).Error
}

func (r *Repository) GetSession(id uint) (*ScanSession, error) {
	var s ScanSession
	err := r.db.First(&s, id).Error
	return &s, err
}

func (r *Repository) ListSessions() ([]ScanSession, error) {
	var sessions []ScanSession
	err := r.db.Order("id DESC").Find(&sessions).Error
	return sessions, err
}

func (r *Repository) ListSessionsByFilesystem(filesystemID uint) ([]ScanSession, error) {
	var sessions []ScanSession
	err := r.db.Where("filesystem_id = ?", filesystemID).Order("id DESC").Find(&sessions).Error
	return sessions, err
}

func (r *Repository) UpdateSession(s *ScanSession) error {
	return r.db.Save(s).Error
}

// UpdateScanProgress persists the live scan counters without touching any
// other column. The scanner calls this every few seconds while walking, so a
// full-row Save of a previously loaded session struct would race the API
// layer's own session writes — column-scoped is the rule for racing writers.
func (r *Repository) UpdateScanProgress(sessionID uint, scanned int, currentPath string) error {
	return r.db.Model(&ScanSession{}).Where("id = ?", sessionID).
		Updates(map[string]interface{}{"scanned_count": scanned, "current_path": currentPath}).Error
}

// ScanInterruptedMessage is the error_message stamped on sessions the startup
// sweep finds stuck in "scanning" from a previous process lifetime.
const ScanInterruptedMessage = "scan interrupted by server restart"

// MarkInterruptedScans flips sessions stuck in "scanning" (process died or was
// killed mid-scan — the walk lives in memory, so nothing will ever finish
// them) to the terminal "interrupted" state. Without this sweep a killed scan
// shows "scanning" forever with zero diagnostics. Returns the rows flipped.
func (r *Repository) MarkInterruptedScans() (int64, error) {
	res := r.db.Model(&ScanSession{}).Where("status = ?", "scanning").
		Updates(map[string]interface{}{
			"status":        "interrupted",
			"error_message": ScanInterruptedMessage,
			"current_path":  "",
		})
	return res.RowsAffected, res.Error
}

func (r *Repository) DeleteSession(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("scan_session_id = ?", id).Delete(&FileEntry{}).Error; err != nil {
			return err
		}
		if err := tx.Where("session_id = ?", id).Delete(&SessionEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("session_id = ?", id).Delete(&AgentRun{}).Error; err != nil {
			return err
		}
		return tx.Delete(&ScanSession{}, id).Error
	})
}

func (r *Repository) RefreshSessionCounts(id uint) error {
	// One query for all FileEntry aggregations.
	var fileCounts struct {
		Total    int64
		Tagged   int64
		Planned  int64
		Executed int64
		Size     int64
	}
	if err := r.db.Model(&FileEntry{}).Where("scan_session_id = ?", id).
		Select(`COUNT(*) AS total,
			SUM(CASE WHEN tagged = ? THEN 1 ELSE 0 END) AS tagged,
			SUM(CASE WHEN operation != '' THEN 1 ELSE 0 END) AS planned,
			SUM(CASE WHEN executed = ? THEN 1 ELSE 0 END) AS executed,
			COALESCE(SUM(size), 0) AS size`, true, true).
		Scan(&fileCounts).Error; err != nil {
		return err
	}

	// Column-scoped update: NEVER a full-row Save. This runs on every session
	// GET, i.e. exactly while a scan/tag/execute goroutine is writing the row
	// — a Save of a previously loaded struct is a lost update that can
	// resurrect status="scanning" over a finished scan (and the startup sweep
	// would then mislabel that completed scan as interrupted).
	return r.db.Model(&ScanSession{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"total_files":  int(fileCounts.Total),
			"tagged_files": int(fileCounts.Tagged),
			"planned_ops":  int(fileCounts.Planned),
			"executed_ops": int(fileCounts.Executed),
			"total_size":   fileCounts.Size,
		}).Error
}

// SessionProgress holds read-only progress counters for live progress events.
type SessionProgress struct {
	Total    int
	Tagged   int
	Planned  int
	Executed int
}

// GetSessionProgress returns the session's file counters without writing the
// session row. Unlike RefreshSessionCounts it is read-only, so concurrent
// agent workers can call it after every batch without risking a lost update
// on the session Status field.
func (r *Repository) GetSessionProgress(id uint) (SessionProgress, error) {
	var counts struct {
		Total    int64
		Tagged   int64
		Planned  int64
		Executed int64
	}
	err := r.db.Model(&FileEntry{}).Where("scan_session_id = ?", id).
		Select(`COUNT(*) AS total,
			SUM(CASE WHEN tagged = ? THEN 1 ELSE 0 END) AS tagged,
			SUM(CASE WHEN operation != '' THEN 1 ELSE 0 END) AS planned,
			SUM(CASE WHEN executed = ? THEN 1 ELSE 0 END) AS executed`, true, true).
		Scan(&counts).Error
	return SessionProgress{
		Total:    int(counts.Total),
		Tagged:   int(counts.Tagged),
		Planned:  int(counts.Planned),
		Executed: int(counts.Executed),
	}, err
}

// ============ FileEntry ============

func (r *Repository) CreateFile(f *FileEntry) error {
	return r.db.Create(f).Error
}

func (r *Repository) CreateFiles(files []FileEntry) error {
	if len(files) == 0 {
		return nil
	}
	// One explicit transaction for the whole bulk load: bare CreateInBatches
	// commits every batch, and on bind-mounted SQLite volumes (Docker on
	// macOS) each commit's fsync costs ~100ms — hundreds of batches meant
	// the DB tail of a scan dwarfed the traversal itself.
	return r.db.Transaction(func(tx *gorm.DB) error {
		return tx.CreateInBatches(files, 500).Error
	})
}

func (r *Repository) GetFile(id uint) (*FileEntry, error) {
	var f FileEntry
	err := r.db.First(&f, id).Error
	return &f, err
}

func (r *Repository) GetFileByPath(sessionID uint, path string) (*FileEntry, error) {
	var f FileEntry
	err := r.db.Where("scan_session_id = ? AND original_path = ?", sessionID, path).First(&f).Error
	return &f, err
}

type FileQuery struct {
	SessionID    uint
	ParentPath   *string
	FileType     string
	Tagged       *bool
	Categorized  *bool
	CategoryPath *string
	Search       string
	Page         int
	PageSize     int
	MinID        uint // cursor-based pagination: only return rows with id > MinID
	Role         *string
	NeedsReview  *bool
	Sensitive    *bool
}

func (r *Repository) ListFiles(q FileQuery) ([]FileEntry, int64, error) {
	tx := applyFileFilters(r.db.Model(&FileEntry{}), q)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.PageSize = 50
	}

	var files []FileEntry
	query := tx
	if q.MinID > 0 {
		query = query.Where("id > ?", q.MinID)
	}
	offset := (q.Page - 1) * q.PageSize
	err := query.Order("file_type ASC, name ASC").Offset(offset).Limit(q.PageSize).Find(&files).Error
	return files, total, err
}

// applyFileFilters builds the shared WHERE clause for FileQuery consumers
// (ListFiles, SampleFiles). Pagination/cursor fields are left to the caller.
func applyFileFilters(tx *gorm.DB, q FileQuery) *gorm.DB {
	if q.SessionID > 0 {
		tx = tx.Where("scan_session_id = ?", q.SessionID)
	}
	if q.ParentPath != nil {
		tx = tx.Where("parent_path = ?", *q.ParentPath)
	}
	if q.FileType != "" {
		tx = tx.Where("file_type = ?", q.FileType)
	}
	if q.Tagged != nil {
		tx = tx.Where("tagged = ?", *q.Tagged)
	}
	if q.Categorized != nil {
		if *q.Categorized {
			tx = tx.Where("new_path != ''")
		} else {
			tx = tx.Where("new_path = ''")
		}
	}
	if q.CategoryPath != nil {
		tx = tx.Where("new_path LIKE ?"+likeEscapeClause, likeLiteral(*q.CategoryPath)+"%")
	}
	if q.Role != nil {
		tx = tx.Where("role = ?", *q.Role)
	}
	if q.NeedsReview != nil {
		tx = tx.Where("needs_review = ?", *q.NeedsReview)
	}
	if q.Sensitive != nil {
		tx = tx.Where("sensitive = ?", *q.Sensitive)
	}
	if q.Search != "" {
		tx = tx.Where(nameSearchCondition(tx), "%"+likeLiteral(q.Search)+"%")
	}
	return tx
}

// SampleFiles returns up to n randomly chosen files matching the query
// filters. It plucks matching IDs, shuffles them in Go (keeps the SQL
// dialect-agnostic — RANDOM()/RAND() differ), then loads and path-sorts the
// winners so the output reads deterministically.
func (r *Repository) SampleFiles(q FileQuery, n int) ([]FileEntry, error) {
	if n <= 0 {
		return nil, nil
	}
	var ids []uint
	if err := applyFileFilters(r.db.Model(&FileEntry{}), q).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	rand.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	if len(ids) > n {
		ids = ids[:n]
	}
	var files []FileEntry
	if len(ids) == 0 {
		return files, nil
	}
	err := r.db.Where("id IN ?", ids).Order("original_path ASC").Find(&files).Error
	return files, err
}

func (r *Repository) UpdateFile(f *FileEntry) error {
	return r.db.Save(f).Error
}

// UpdateDescription writes only the description column. The tool layer runs
// concurrently with other tools on the same row (eino executes parallel tool
// calls), so a full-row Save of a previously loaded struct would clobber
// concurrent field writes (e.g. a racing set_target's new_path).
func (r *Repository) UpdateDescription(sessionID uint, path, description string) error {
	return r.db.Model(&FileEntry{}).
		Where("scan_session_id = ? AND original_path = ?", sessionID, path).
		Update("description", description).Error
}

// TaggedStatusByIDs returns the tagged flag for each given file ID. It backs
// the loose-file batch re-check: a batch can finish successfully while leaving
// files untagged (set_target does not flip tagged), and such misses must be
// detectable after the fact.
func (r *Repository) TaggedStatusByIDs(ids []uint) (map[uint]bool, error) {
	out := make(map[uint]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []FileEntry
	if err := r.db.Select("id", "tagged").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, f := range rows {
		out[f.ID] = f.Tagged
	}
	return out, nil
}

// UntaggedBreakdown splits a session's untagged entries by file type. It feeds
// the end-of-run reconciliation line and the loose-file re-sweep: untagged
// directories may be containers deliberately left for their children and
// symlinks are never tagged — only the file count signals tagging misses.
type UntaggedBreakdown struct {
	Directories int64
	Files       int64
	Symlinks    int64
}

// Total returns the total number of untagged entries across all types.
func (b UntaggedBreakdown) Total() int64 {
	return b.Directories + b.Files + b.Symlinks
}

// CountUntaggedByType counts a session's untagged entries split by file type.
func (r *Repository) CountUntaggedByType(sessionID uint) (UntaggedBreakdown, error) {
	var b UntaggedBreakdown
	err := r.db.Model(&FileEntry{}).
		Where("scan_session_id = ? AND tagged = ?", sessionID, false).
		Select(`SUM(CASE WHEN file_type = 'directory' THEN 1 ELSE 0 END) AS directories,
			SUM(CASE WHEN file_type = 'file' THEN 1 ELSE 0 END) AS files,
			SUM(CASE WHEN file_type = 'symlink' THEN 1 ELSE 0 END) AS symlinks`).
		Scan(&b).Error
	return b, err
}

// MarkJunkEntries auto-ignores well-known system junk before tagging: files
// whose base name matches fileNames (ASCII-lowered) or the AppleDouble "._*"
// prefix, and directories matching dirNames with all their descendants.
// Matched rows are flipped to tagged with the JunkBatchIndex sentinel and an
// explanatory description, so they never reach an agent batch. Returns the
// number of rows ignored.
func (r *Repository) MarkJunkEntries(sessionID uint, fileNames, dirNames []string) (int64, error) {
	var total int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&FileEntry{}).
			Where("scan_session_id = ? AND role = ? AND file_type = 'file' AND (lower(name) IN ? OR name LIKE '.!_%'"+likeEscapeClause+")", sessionID, RoleUndecided, fileNames).
			Updates(map[string]interface{}{"tagged": true, "role": RoleSkip, "reason": "系统垃圾文件", "batch_index": JunkBatchIndex})
		if res.Error != nil {
			return res.Error
		}
		total += res.RowsAffected

		var dirs []FileEntry
		if err := tx.Where("scan_session_id = ? AND role = ? AND file_type = 'directory' AND lower(name) IN ?", sessionID, RoleUndecided, dirNames).
			Find(&dirs).Error; err != nil {
			return err
		}
		for _, d := range dirs {
			if err := tx.Model(&FileEntry{}).Where("id = ?", d.ID).
				Updates(map[string]interface{}{"tagged": true, "role": RoleSkip, "reason": "系统垃圾目录", "batch_index": JunkBatchIndex}).Error; err != nil {
				return err
			}
			res := tx.Model(&FileEntry{}).
				Where("scan_session_id = ? AND original_path LIKE ?"+likeEscapeClause, sessionID, childPathPattern(d.OriginalPath)).
				Updates(map[string]interface{}{"tagged": true, "role": RoleMember, "reason": MemberReason(d.OriginalPath, RoleSkip), "new_path": "", "operation": "", "batch_index": JunkBatchIndex})
			if res.Error != nil {
				return res.Error
			}
			total += res.RowsAffected + 1
		}
		return nil
	})
	return total, err
}

// CountIgnoredJunk returns how many entries the junk filter has auto-ignored
// in this session (the JunkBatchIndex sentinel rows).
func (r *Repository) CountIgnoredJunk(sessionID uint) (int64, error) {
	var n int64
	err := r.db.Model(&FileEntry{}).
		Where("scan_session_id = ? AND batch_index = ?", sessionID, JunkBatchIndex).
		Count(&n).Error
	return n, err
}

// CountPlannedByTargetPath counts planned files under a category path across
// ALL sessions of a filesystem — the filesystem-level usage view. The
// empty-category prune must use this (not the per-session summary): a
// category filled by an earlier run shows zero in the current session yet is
// genuinely in use (multi-session rescan workflows).
func (r *Repository) CountPlannedByTargetPath(filesystemID uint, categoryPath string) (int64, error) {
	var n int64
	err := r.db.Model(&FileEntry{}).
		Where("scan_session_id IN (SELECT id FROM scan_sessions WHERE filesystem_id = ?) AND operation != '' AND (new_path = ? OR new_path LIKE ?"+likeEscapeClause+")", filesystemID, categoryPath, childPathPattern(categoryPath)).
		Count(&n).Error
	return n, err
}

// ClearPlannedByTargetPath clears new_path/operation and resets tagged for files
// whose new_path falls under the given category path, across all sessions of a filesystem.
func (r *Repository) ClearPlannedByTargetPath(filesystemID uint, categoryPath string) error {
	return r.db.Model(&FileEntry{}).
		Where("scan_session_id IN (SELECT id FROM scan_sessions WHERE filesystem_id = ?) AND (new_path = ? OR new_path LIKE ?"+likeEscapeClause+")", filesystemID, categoryPath, childPathPattern(categoryPath)).
		Updates(map[string]interface{}{"new_path": "", "operation": "", "tagged": false}).Error
}

func (r *Repository) GetPlannedFiles(sessionID uint) ([]FileEntry, error) {
	var files []FileEntry
	err := r.db.Where("scan_session_id = ? AND operation != '' AND executed = ? AND needs_review = ?", sessionID, false, false).
		Find(&files).Error
	return files, err
}

func (r *Repository) GetTreeNodes(sessionID uint, parentPath string) ([]FileEntry, error) {
	var files []FileEntry
	err := r.db.Where("scan_session_id = ? AND parent_path = ? AND file_type = ?", sessionID, parentPath, "directory").
		Order("name ASC").
		Find(&files).Error
	return files, err
}

// ============ Category ============

func (r *Repository) CreateCategory(c *Category) error {
	return r.db.Create(c).Error
}

// CreateCategoryWithConflict creates a category and returns whether it already existed.
func (r *Repository) CreateCategoryWithConflict(c *Category) (created bool, err error) {
	err = r.db.Create(c).Error
	if err != nil {
		// Check for unique constraint violation (name or path already exists)
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *Repository) GetCategory(id uint) (*Category, error) {
	var c Category
	err := r.db.First(&c, id).Error
	return &c, err
}

func (r *Repository) ListCategories(filesystemID uint) ([]Category, error) {
	var cats []Category
	err := r.db.Where("filesystem_id = ?", filesystemID).Order("name ASC").Find(&cats).Error
	return cats, err
}

func (r *Repository) GetCategoryByName(filesystemID uint, name string) (*Category, error) {
	var cat Category
	err := r.db.Where("filesystem_id = ? AND name = ?", filesystemID, name).First(&cat).Error
	return &cat, err
}

// CategoryUsage holds a category's name, path, and how many files are planned under it.
type CategoryUsage struct {
	Name        string
	Path        string
	Description string
	Structure   string
	FileCount   int64
}

// GetCategoryUsageSummary returns every category of the filesystem with the
// count of files planned under its path in the given session. Categories with
// no planned files are included with a zero count so callers (the agent prompt
// summary) always see the complete classification system. The count matches
// new_path == path or new_path under path + "/" exactly, so sibling prefixes
// ("/photo" vs "/photos2") do not bleed into each other.
func (r *Repository) GetCategoryUsageSummary(filesystemID uint, sessionID uint) ([]CategoryUsage, error) {
	cats, err := r.ListCategories(filesystemID)
	if err != nil {
		return nil, err
	}
	result := make([]CategoryUsage, 0, len(cats))
	for _, c := range cats {
		var count int64
		if err := r.db.Model(&FileEntry{}).
			Where("scan_session_id = ? AND (new_path = ? OR new_path LIKE ?"+likeEscapeClause+")", sessionID, c.Path, childPathPattern(c.Path)).
			Count(&count).Error; err != nil {
			return nil, err
		}
		result = append(result, CategoryUsage{Name: c.Name, Path: c.Path, Description: c.Description, Structure: c.Structure, FileCount: count})
	}
	return result, nil
}

func (r *Repository) UpdateCategory(c *Category) error {
	return r.db.Save(c).Error
}

// UpdateCategoryPath updates the category and cascades the path change to all files
// whose new_path starts with the old path prefix.
func (r *Repository) UpdateCategoryPath(c *Category, oldPath string) error {
	if err := r.db.Save(c).Error; err != nil {
		return err
	}
	if oldPath != "" && oldPath != c.Path {
		// Update all files whose new_path starts with oldPath
		return r.db.Model(&FileEntry{}).
			Where("new_path LIKE ?"+likeEscapeClause, likeLiteral(oldPath)+"%").
			Update("new_path", gorm.Expr("REPLACE(new_path, ?, ?)", oldPath, c.Path)).Error
	}
	return nil
}

func (r *Repository) DeleteCategory(id uint) error {
	return r.db.Delete(&Category{}, id).Error
}

// ============ Filesystem ============

func (r *Repository) CreateFilesystem(fs *Filesystem) error {
	return r.db.Create(fs).Error
}

func (r *Repository) GetFilesystem(id uint) (*Filesystem, error) {
	var fs Filesystem
	err := r.db.First(&fs, id).Error
	return &fs, err
}

func (r *Repository) ListFilesystems() ([]Filesystem, error) {
	var fss []Filesystem
	err := r.db.Order("name ASC").Find(&fss).Error
	return fss, err
}

func (r *Repository) UpdateFilesystem(fs *Filesystem) error {
	return r.db.Save(fs).Error
}

func (r *Repository) DeleteFilesystem(id uint) error {
	return r.db.Delete(&Filesystem{}, id).Error
}

// ============ ModelProvider ============

func (r *Repository) CreateModelProvider(m *ModelProvider) error {
	return r.db.Create(m).Error
}

func (r *Repository) GetModelProvider(id uint) (*ModelProvider, error) {
	var m ModelProvider
	err := r.db.First(&m, id).Error
	return &m, err
}

func (r *Repository) ListModelProviders() ([]ModelProvider, error) {
	var providers []ModelProvider
	err := r.db.Order("id ASC").Find(&providers).Error
	return providers, err
}

func (r *Repository) UpdateModelProvider(m *ModelProvider) error {
	return r.db.Save(m).Error
}

func (r *Repository) DeleteModelProvider(id uint) error {
	return r.db.Delete(&ModelProvider{}, id).Error
}

// ============ Batch cursor ============

// NextManualBatchIndex returns a fresh negative batch index for a
// user-directed run, below JunkBatchIndex and every manual index in use.
func (r *Repository) NextManualBatchIndex(sessionID uint) int {
	var minBatch *int
	r.db.Model(&FileEntry{}).
		Where("scan_session_id = ? AND batch_index < ?", sessionID, JunkBatchIndex).
		Select("MIN(batch_index)").Scan(&minBatch)
	if minBatch == nil {
		return JunkBatchIndex - 1
	}
	return *minBatch - 1
}

// CategoryDistribution holds a category name/path and the count+size of files planned under it.
type CategoryDistribution struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	FileCount int64  `json:"file_count"`
	TotalSize int64  `json:"total_size"`
}

// GetSessionStats returns category distribution for a session.
func (r *Repository) GetSessionStats(sessionID uint, filesystemID uint) ([]CategoryDistribution, error) {
	cats, err := r.ListCategories(filesystemID)
	if err != nil {
		return nil, err
	}
	var result []CategoryDistribution
	for _, c := range cats {
		var stats struct {
			Count int64
			Size  int64
		}
		r.db.Model(&FileEntry{}).
			Where("scan_session_id = ? AND (new_path = ? OR new_path LIKE ?"+likeEscapeClause+")", sessionID, c.Path, childPathPattern(c.Path)).
			Select("COUNT(*) AS count, COALESCE(SUM(size), 0) AS size").
			Scan(&stats)
		if stats.Count > 0 {
			result = append(result, CategoryDistribution{
				Name: c.Name, Path: c.Path,
				FileCount: stats.Count, TotalSize: stats.Size,
			})
		}
	}
	return result, nil
}

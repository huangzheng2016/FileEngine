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

func (r *Repository) DeleteSession(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("scan_session_id = ?", id).Delete(&FileEntry{}).Error; err != nil {
			return err
		}
		if err := tx.Where("scan_session_id = ?", id).Delete(&AgentLog{}).Error; err != nil {
			return err
		}
		return tx.Delete(&ScanSession{}, id).Error
	})
}

func (r *Repository) DeleteFilesBySession(sessionID uint) error {
	return r.db.Where("scan_session_id = ?", sessionID).Delete(&FileEntry{}).Error
}

func (r *Repository) RefreshSessionCounts(id uint) error {
	var session ScanSession
	if err := r.db.First(&session, id).Error; err != nil {
		return err
	}

	// Single query for all FileEntry aggregations (was 5 separate queries)
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

	// Single query for all AgentLog token sums (was 3 separate queries)
	var tokenSums struct {
		PromptTokens     int64
		CompletionTokens int64
		TotalTokens      int64
	}
	if err := r.db.Model(&AgentLog{}).Where("scan_session_id = ?", id).
		Select(`COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(total_tokens), 0) AS total_tokens`).
		Scan(&tokenSums).Error; err != nil {
		return err
	}

	session.TotalFiles = int(fileCounts.Total)
	session.TaggedFiles = int(fileCounts.Tagged)
	session.PlannedOps = int(fileCounts.Planned)
	session.ExecutedOps = int(fileCounts.Executed)
	session.TotalSize = fileCounts.Size
	session.PromptTokens = int(tokenSums.PromptTokens)
	session.CompletionTokens = int(tokenSums.CompletionTokens)
	session.TotalTokens = int(tokenSums.TotalTokens)
	return r.db.Save(&session).Error
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
	return r.db.CreateInBatches(files, 100).Error
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
	// IDMod/IDModRem shard the result set deterministically by primary key
	// (id % IDMod = IDModRem); harness workers pull disjoint shards of the
	// untagged queue this way. IDMod <= 1 disables sharding.
	IDMod    int
	IDModRem int
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
	if q.Search != "" {
		tx = tx.Where(nameSearchCondition(tx), "%"+likeLiteral(q.Search)+"%")
	}
	if q.IDMod > 1 {
		tx = tx.Where("id % ? = ?", q.IDMod, q.IDModRem)
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

func (r *Repository) GetUntaggedDirectories(sessionID uint, batchSize int) ([]FileEntry, error) {
	var dirs []FileEntry
	err := r.db.Where("scan_session_id = ? AND file_type = 'directory' AND tagged = ?", sessionID, false).
		Order("depth DESC").
		Limit(batchSize).
		Find(&dirs).Error
	return dirs, err
}

func (r *Repository) GetUntaggedDirectoriesAtDepth(sessionID uint, depth int) ([]FileEntry, error) {
	var dirs []FileEntry
	err := r.db.Where("scan_session_id = ? AND file_type = 'directory' AND tagged = ? AND depth = ?",
		sessionID, false, depth).
		Find(&dirs).Error
	return dirs, err
}

func (r *Repository) GetMaxDepth(sessionID uint) (int, error) {
	var maxDepth *int
	err := r.db.Model(&FileEntry{}).
		Where("scan_session_id = ?", sessionID).
		Select("MAX(depth)").
		Scan(&maxDepth).Error
	if err != nil {
		return 0, err
	}
	if maxDepth == nil {
		return 0, nil
	}
	return *maxDepth, nil
}

func (r *Repository) IsFileTagged(sessionID uint, path string) (bool, error) {
	var f FileEntry
	err := r.db.Select("tagged").
		Where("scan_session_id = ? AND original_path = ?", sessionID, path).
		First(&f).Error
	if err != nil {
		return false, err
	}
	return f.Tagged, nil
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

// ClaimDirectory atomically claims an untagged directory for processing by setting its batch_index.
// Returns true if the claim succeeded (directory was untagged), false if already claimed or tagged.
func (r *Repository) ClaimDirectory(sessionID uint, path string, batchIndex int) (bool, error) {
	result := r.db.Model(&FileEntry{}).
		Where("scan_session_id = ? AND original_path = ? AND tagged = ?", sessionID, path, false).
		Updates(map[string]interface{}{"batch_index": batchIndex})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *Repository) MarkChildrenTagged(sessionID uint, parentPath string, batchIndex int) error {
	return r.db.Model(&FileEntry{}).
		Where("scan_session_id = ? AND original_path LIKE ?"+likeEscapeClause, sessionID, childPathPattern(parentPath)).
		Updates(map[string]interface{}{"tagged": true, "batch_index": batchIndex}).Error
}

// MarkTaggedAtomic atomically marks a directory and all its children as tagged within a transaction.
func (r *Repository) MarkTaggedAtomic(sessionID uint, path string, batchIndex int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&FileEntry{}).
			Where("scan_session_id = ? AND original_path = ?", sessionID, path).
			Updates(map[string]interface{}{"tagged": true, "batch_index": batchIndex}).Error; err != nil {
			return err
		}
		return tx.Model(&FileEntry{}).
			Where("scan_session_id = ? AND original_path LIKE ?"+likeEscapeClause, sessionID, childPathPattern(path)).
			Updates(map[string]interface{}{"tagged": true, "batch_index": batchIndex}).Error
	})
}

func (r *Repository) ClearChildrenTarget(sessionID uint, parentPath string) error {
	return r.db.Model(&FileEntry{}).
		Where("scan_session_id = ? AND original_path LIKE ?"+likeEscapeClause+" AND operation != ''", sessionID, childPathPattern(parentPath)).
		Updates(map[string]interface{}{"new_path": "", "operation": ""}).Error
}

// SetTargetAtomic atomically sets a file's target and clears children's targets (for directories) within a transaction.
// It returns how many already-planned children the cascade cleared (outer
// overrides inner) so the caller can surface that consequence to the agent.
func (r *Repository) SetTargetAtomic(sessionID uint, path string, newPath string, operation string, fileType string) (int64, error) {
	var cleared int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&FileEntry{}).
			Where("scan_session_id = ? AND original_path = ?", sessionID, path).
			Updates(map[string]interface{}{"new_path": newPath, "operation": operation}).Error; err != nil {
			return err
		}
		if fileType == "directory" && newPath != "" {
			res := tx.Model(&FileEntry{}).
				Where("scan_session_id = ? AND original_path LIKE ?"+likeEscapeClause+" AND operation != ''", sessionID, childPathPattern(path)).
				Updates(map[string]interface{}{"new_path": "", "operation": ""})
			cleared = res.RowsAffected
			return res.Error
		}
		return nil
	})
	return cleared, err
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
			Where("scan_session_id = ? AND tagged = ? AND file_type = 'file' AND (lower(name) IN ? OR name LIKE '.!_%'"+likeEscapeClause+")", sessionID, false, fileNames).
			Updates(map[string]interface{}{"tagged": true, "batch_index": JunkBatchIndex, "description": "系统垃圾文件（自动忽略）"})
		if res.Error != nil {
			return res.Error
		}
		total += res.RowsAffected

		var dirs []FileEntry
		if err := tx.Where("scan_session_id = ? AND tagged = ? AND file_type = 'directory' AND lower(name) IN ?", sessionID, false, dirNames).
			Find(&dirs).Error; err != nil {
			return err
		}
		for _, d := range dirs {
			res := tx.Model(&FileEntry{}).
				Where("scan_session_id = ? AND tagged = ? AND (original_path = ? OR original_path LIKE ?"+likeEscapeClause+")", sessionID, false, d.OriginalPath, childPathPattern(d.OriginalPath)).
				Updates(map[string]interface{}{"tagged": true, "batch_index": JunkBatchIndex, "description": "系统垃圾目录（自动忽略）"})
			if res.Error != nil {
				return res.Error
			}
			total += res.RowsAffected
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

// ClearPlannedByTargetPath clears new_path/operation and resets tagged for files
// whose new_path falls under the given category path, across all sessions of a filesystem.
func (r *Repository) ClearPlannedByTargetPath(filesystemID uint, categoryPath string) error {
	return r.db.Model(&FileEntry{}).
		Where("scan_session_id IN (SELECT id FROM scan_sessions WHERE filesystem_id = ?) AND (new_path = ? OR new_path LIKE ?"+likeEscapeClause+")", filesystemID, categoryPath, childPathPattern(categoryPath)).
		Updates(map[string]interface{}{"new_path": "", "operation": "", "tagged": false}).Error
}

func (r *Repository) GetPlannedFiles(sessionID uint) ([]FileEntry, error) {
	var files []FileEntry
	err := r.db.Where("scan_session_id = ? AND operation != '' AND executed = ?", sessionID, false).
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
	Name      string
	Path      string
	FileCount int64
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
		result = append(result, CategoryUsage{Name: c.Name, Path: c.Path, FileCount: count})
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

// ============ AgentLog ============

func (r *Repository) CreateAgentLog(log *AgentLog) error {
	return r.db.Create(log).Error
}

func (r *Repository) CreateAgentLogs(logs []*AgentLog) error {
	if len(logs) == 0 {
		return nil
	}
	return r.db.CreateInBatches(logs, 100).Error
}

type LogQuery struct {
	SessionID uint
	Batch     *int
	Role      string
	ToolName  string
	Order     string // "asc" or "desc"
	Page      int
	PageSize  int
}

func (r *Repository) ListAgentLogs(q LogQuery) ([]AgentLog, int64, error) {
	tx := r.db.Model(&AgentLog{})
	if q.SessionID > 0 {
		tx = tx.Where("scan_session_id = ?", q.SessionID)
	}
	if q.Batch != nil {
		tx = tx.Where("batch_index = ?", *q.Batch)
	}
	if q.Role != "" {
		tx = tx.Where("role = ?", q.Role)
	}
	if q.ToolName != "" {
		tx = tx.Where("tool_name = ?", q.ToolName)
	}

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
	offset := (q.Page - 1) * q.PageSize

	order := "id ASC"
	if q.Order == "desc" {
		order = "id DESC"
	}

	var logs []AgentLog
	err := tx.Order(order).Offset(offset).Limit(q.PageSize).Find(&logs).Error
	return logs, total, err
}

type BatchInfo struct {
	BatchIndex int    `json:"batch_index"`
	CreatedAt  string `json:"created_at"`
}

func (r *Repository) NextManualBatchIndex(sessionID uint) int {
	var minBatch *int
	r.db.Model(&AgentLog{}).
		Where("scan_session_id = ? AND batch_index < 0", sessionID).
		Select("MIN(batch_index)").Scan(&minBatch)
	if minBatch == nil {
		return -1
	}
	return *minBatch - 1
}

func (r *Repository) MaxBatchIndex(sessionID uint) int {
	var maxBatch *int
	r.db.Model(&AgentLog{}).
		Where("scan_session_id = ? AND batch_index > 0", sessionID).
		Select("MAX(batch_index)").Scan(&maxBatch)
	if maxBatch == nil {
		return 0
	}
	return *maxBatch
}

func (r *Repository) ListBatches(sessionID uint, page, pageSize int) ([]BatchInfo, int64, error) {
	base := r.db.Model(&AgentLog{}).Where("scan_session_id = ?", sessionID)

	var total int64
	if err := base.Distinct("batch_index").Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize

	var batches []BatchInfo
	err := r.db.Model(&AgentLog{}).
		Select("batch_index, MIN(created_at) as created_at").
		Where("scan_session_id = ?", sessionID).
		Group("batch_index").
		Order("MIN(created_at) ASC").
		Offset(offset).Limit(pageSize).
		Find(&batches).Error
	return batches, total, err
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

package db

import (
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

// RefreshSessionCounts recomputes the stored session counters and writes
// them only when they changed. It is never called on a read path: writers
// call it at the end of a phase, and entry writes schedule it through
// touchCounts.
func (r *Repository) RefreshSessionCounts(id uint) error { return r.refreshCounts(r.Reader(), id) }

func (r *Repository) refreshCounts(reader *gorm.DB, id uint) error {
	var c struct {
		Total    int64
		Tagged   int64
		Planned  int64
		Executed int64
		Size     int64
	}
	if err := reader.Model(&FileEntry{}).Where("scan_session_id = ?", id).
		Select(`COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN tagged = ? THEN 1 ELSE 0 END), 0) AS tagged,
			COALESCE(SUM(CASE WHEN operation != '' THEN 1 ELSE 0 END), 0) AS planned,
			COALESCE(SUM(CASE WHEN executed = ? THEN 1 ELSE 0 END), 0) AS executed,
			COALESCE(SUM(size), 0) AS size`, true, true).
		Scan(&c).Error; err != nil {
		return err
	}
	// Column-scoped (never a Save of a loaded session, which would race the
	// scan/run writers' status updates); the WHERE makes an unchanged row a
	// no-op.
	return r.db.Model(&ScanSession{}).
		Where("id = ? AND (total_files <> ? OR tagged_files <> ? OR planned_ops <> ? OR executed_ops <> ? OR total_size <> ?)",
			id, c.Total, c.Tagged, c.Planned, c.Executed, c.Size).
		Updates(map[string]any{
			"total_files":  c.Total,
			"tagged_files": c.Tagged,
			"planned_ops":  c.Planned,
			"executed_ops": c.Executed,
			"total_size":   c.Size,
		}).Error
}

// countsDelay coalesces entry writes before a session's counters are
// recomputed.
var countsDelay = time.Second

type countsKey struct {
	db *gorm.DB
	id uint
}

var countsPending sync.Map

// touchCounts schedules one coalesced RefreshSessionCounts after entry
// writes, so per-decision writes never pay for a full aggregate.
func (r *Repository) touchCounts(sessionID uint) {
	k := countsKey{r.db, sessionID}
	if _, loaded := countsPending.LoadOrStore(k, struct{}{}); loaded {
		return
	}
	reader := r.Reader() // resolved here: the timer must not read the package globals
	time.AfterFunc(countsDelay, func() {
		countsPending.Delete(k)
		if err := WithRetry(func() error { return r.refreshCounts(reader, sessionID) }); err != nil {
			log.Printf("refresh counts of session %d: %v", sessionID, err)
		}
	})
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
	err := r.Reader().Model(&FileEntry{}).Where("scan_session_id = ?", id).
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
		return tx.CreateInBatches(files, r.InsertBatchRows()).Error
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
	Role         *string
	NeedsReview  *bool
	Sensitive    *bool
}

// fileOrder is the listing order; id makes it total so pages never overlap.
const fileOrder = "file_type ASC, name ASC, id ASC"

// ListFiles returns one offset page plus the total count (the REST listing,
// whose UI shows page numbers). Iterating callers use ListFilesAfter.
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
	offset := (q.Page - 1) * q.PageSize
	err := tx.Order(fileOrder).Offset(offset).Limit(q.PageSize).Find(&files).Error
	return files, total, err
}

// ErrBadCursor reports a keyset cursor whose entry no longer exists.
var ErrBadCursor = errors.New("bad cursor")

// ListFilesAfter is keyset pagination over q in fileOrder: the page after
// entry afterID (0 = first page) and whether more rows follow, without a
// COUNT. For a directory listing idx_session_parent serves both the filter
// and the order, so every page costs the same.
func (r *Repository) ListFilesAfter(q FileQuery, afterID uint, limit int) ([]FileEntry, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	tx := applyFileFilters(r.db.Model(&FileEntry{}), q)
	if afterID > 0 {
		var last []FileEntry
		if err := r.db.Select("id", "file_type", "name").Where("scan_session_id = ? AND id = ?", q.SessionID, afterID).Limit(1).Find(&last).Error; err != nil {
			return nil, false, err
		}
		if len(last) == 0 {
			return nil, false, ErrBadCursor
		}
		tx = tx.Where("(file_type, name, id) > (?, ?, ?)", last[0].FileType, last[0].Name, last[0].ID)
	}
	var files []FileEntry
	if err := tx.Order(fileOrder).Limit(limit + 1).Find(&files).Error; err != nil {
		return nil, false, err
	}
	if len(files) > limit {
		return files[:limit], true, nil
	}
	return files, false, nil
}

// applyFileFilters builds the shared WHERE clause for FileQuery consumers
// (ListFiles, ListFilesAfter, SampleFiles). Pagination is left to the caller.
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
		cond, args := targetCond(tx, *q.CategoryPath)
		tx = tx.Where(cond, args...)
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
// filters, path-sorted. It probes random points of the matching id range
// (each probe is one indexed "id >= x LIMIT 1") instead of loading every id;
// rows after id gaps are slightly favored, which sampling tolerates.
func (r *Repository) SampleFiles(q FileQuery, n int) ([]FileEntry, error) {
	if n <= 0 {
		return nil, nil
	}
	base := func() *gorm.DB { return applyFileFilters(r.db.Model(&FileEntry{}), q) }
	var head []uint
	if err := base().Order("id").Limit(n+1).Pluck("id", &head).Error; err != nil {
		return nil, err
	}
	ids := head
	if len(head) > n {
		var maxID uint
		if err := base().Select("MAX(id)").Scan(&maxID).Error; err != nil {
			return nil, err
		}
		lo := head[0]
		seen := make(map[uint]bool, n)
		ids = make([]uint, 0, n)
		for tries := 0; len(ids) < n && tries < 4*n; tries++ {
			probe := lo + uint(rand.Uint64N(uint64(maxID-lo)+1))
			var hit []uint
			if err := base().Where("id >= ?", probe).Order("id").Limit(1).Pluck("id", &hit).Error; err != nil {
				return nil, err
			}
			if len(hit) == 1 && !seen[hit[0]] {
				seen[hit[0]] = true
				ids = append(ids, hit[0])
			}
		}
		for _, id := range head {
			if len(ids) >= n {
				break
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
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
			res := entriesUnder(tx, sessionID, d.OriginalPath).
				Updates(map[string]interface{}{"tagged": true, "role": RoleMember, "reason": MemberReason(d.OriginalPath, RoleSkip), "new_path": "", "new_path_key": "", "operation": "", "batch_index": JunkBatchIndex})
			if res.Error != nil {
				return res.Error
			}
			total += res.RowsAffected + 1
		}
		return nil
	})
	if err == nil && total > 0 {
		r.touchCounts(sessionID)
	}
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
	cond, args := targetCond(r.db, categoryPath)
	err := r.db.Model(&FileEntry{}).
		Where("scan_session_id IN (SELECT id FROM scan_sessions WHERE filesystem_id = ?) AND operation != ''", filesystemID).
		Where(cond, args...).
		Count(&n).Error
	return n, err
}

// ClearPlannedByTargetPath returns every entry targeted at or under the
// category path, across all sessions of a filesystem, to undecided; members of
// a cleared unit are released with it.
func (r *Repository) ClearPlannedByTargetPath(filesystemID uint, categoryPath string) error {
	sessions, err := r.filesystemSessionIDs(filesystemID)
	if err != nil || len(sessions) == 0 {
		return err
	}
	defer func() {
		for _, id := range sessions {
			r.touchCounts(id)
		}
	}()
	return WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			cond, args := targetCond(tx, categoryPath)
			var units []FileEntry
			if err := tx.Select("scan_session_id", "original_path").
				Where("scan_session_id IN ? AND role = ?", sessions, RoleUnit).Where(cond, args...).
				Find(&units).Error; err != nil {
				return err
			}
			for _, u := range units {
				if err := releaseMembers(tx, u.ScanSessionID, u.OriginalPath).Error; err != nil {
					return err
				}
			}
			return tx.Model(&FileEntry{}).Where("scan_session_id IN ?", sessions).Where(cond, args...).
				Updates(undecidedFields()).Error
		})
	})
}

func (r *Repository) filesystemSessionIDs(filesystemID uint) ([]uint, error) {
	var ids []uint
	err := r.db.Model(&ScanSession{}).Where("filesystem_id = ?", filesystemID).Pluck("id", &ids).Error
	return ids, err
}

// categoryDir is a category path in the stored target spelling without a
// trailing slash ("/" becomes "", the whole target tree).
func categoryDir(p string) string {
	return strings.TrimSuffix(NormalizeTargetPath(p), "/")
}

// targetCond matches new_path at or under a category path; "/" sibling
// prefixes ("/photo" vs "/photos2") never match.
func targetCond(tx *gorm.DB, categoryPath string) (string, []any) {
	dir := categoryDir(categoryPath)
	if dir == "" {
		return subtreeCond(tx, "new_path", "")
	}
	return atOrUnderCond(tx, "new_path", dir)
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
	stats, err := r.categoryStats(sessionID, cats)
	if err != nil {
		return nil, err
	}
	result := make([]CategoryUsage, len(cats))
	for i, c := range cats {
		result[i] = CategoryUsage{Name: c.Name, Path: c.Path, Description: c.Description, Structure: c.Structure, FileCount: stats[i].Count}
	}
	return result, nil
}

type categoryStat struct {
	Idx   int
	Count int64
	Size  int64
}

// categoryStatsChunk bounds the UNION ALL arms of one statement (SQLite caps
// compound selects at 500).
const categoryStatsChunk = 200

// categoryStats counts the session's targets at or under each category path
// (and sums their sizes) in one UNION ALL statement per chunk; every arm is an
// index range on idx_session_newpath.
func (r *Repository) categoryStats(sessionID uint, cats []Category) ([]categoryStat, error) {
	out := make([]categoryStat, len(cats))
	for start := 0; start < len(cats); start += categoryStatsChunk {
		end := min(start+categoryStatsChunk, len(cats))
		arms := make([]string, 0, end-start)
		var args []any
		for i := start; i < end; i++ {
			cond, cargs := targetCond(r.db, cats[i].Path)
			arms = append(arms, fmt.Sprintf("SELECT %d AS idx, COUNT(*) AS count, COALESCE(SUM(size), 0) AS size FROM file_entries WHERE scan_session_id = ? AND %s", i, cond))
			args = append(append(args, sessionID), cargs...)
		}
		var rows []categoryStat
		if err := r.db.Raw(strings.Join(arms, " UNION ALL "), args...).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[row.Idx] = row
		}
	}
	return out, nil
}

func (r *Repository) UpdateCategory(c *Category) error {
	return r.db.Save(c).Error
}

// UpdateCategoryPath saves the category and moves the targets at or under
// its old path to the new one, in the filesystem's sessions only. The prefix
// is swapped by position, so "/photo" → "/pics" leaves "/photos2" alone and a
// repeated substring deeper in the path is never touched.
func (r *Repository) UpdateCategoryPath(c *Category, oldPath string) error {
	if err := r.db.Save(c).Error; err != nil {
		return err
	}
	from, to := categoryDir(oldPath), categoryDir(c.Path)
	if from == "" || to == "" || from == to {
		return nil
	}
	sessions, err := r.filesystemSessionIDs(c.FilesystemID)
	if err != nil || len(sessions) == 0 {
		return err
	}
	cond, args := atOrUnderCond(r.db, "new_path", from)
	return WithRetry(func() error {
		return r.db.Model(&FileEntry{}).Where("scan_session_id IN ?", sessions).Where(cond, args...).
			Updates(map[string]any{
				"new_path":     gorm.Expr(concatExpr(r.db, "?", "substr(new_path, ?)"), to, utf8.RuneCountInString(from)+1),
				"new_path_key": gorm.Expr(concatExpr(r.db, "?", "substr(new_path_key, ?)"), CollisionKey(to), utf8.RuneCountInString(CollisionKey(from))+1),
			}).Error
	})
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
	stats, err := r.categoryStats(sessionID, cats)
	if err != nil {
		return nil, err
	}
	var result []CategoryDistribution
	for i, c := range cats {
		if stats[i].Count > 0 {
			result = append(result, CategoryDistribution{
				Name: c.Name, Path: c.Path,
				FileCount: stats[i].Count, TotalSize: stats[i].Size,
			})
		}
	}
	return result, nil
}

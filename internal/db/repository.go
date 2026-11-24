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

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// WithDB is a repository over another handle, for reads on the read pool.
func (r *Repository) WithDB(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) DB() *gorm.DB {
	return r.db
}

func (r *Repository) GetSession(id uint) (*model.ScanSession, error) {
	var s model.ScanSession
	err := r.db.First(&s, id).Error
	return &s, err
}

// BeginScan marks the session scanning and clears the previous scan's progress
// and messages. Column-scoped: settings the user changes meanwhile are never
// written back.
func (r *Repository) BeginScan(id uint) error {
	return dal.WithRetry(func() error {
		return r.db.Model(&model.ScanSession{}).Where("id = ?", id).Updates(map[string]any{
			"status": "scanning", "scanned_count": 0, "current_path": "", "error_message": "", "scan_warning": "",
		}).Error
	})
}

// FinishScan completes a scan that is still scanning; a session whose scan was
// replaced or failed meanwhile is left alone.
func (r *Repository) FinishScan(id uint, scanned int, warning string) error {
	return dal.WithRetry(func() error {
		return r.db.Model(&model.ScanSession{}).Where("id = ? AND status = ?", id, "scanning").Updates(map[string]any{
			"status": "scanned", "total_files": scanned, "scanned_count": scanned, "current_path": "",
			"error_message": "", "scan_warning": warning,
		}).Error
	})
}

// FailScan records why a scan that is still scanning could not complete.
func (r *Repository) FailScan(id uint, message string) error {
	return dal.WithRetry(func() error {
		return r.db.Model(&model.ScanSession{}).Where("id = ? AND status = ?", id, "scanning").Updates(map[string]any{
			"status": "error", "error_message": message, "current_path": "",
		}).Error
	})
}

// Column-scoped: the scanner calls this every few seconds, and a full-row
// Save would race the API layer's own session writes.
func (r *Repository) UpdateScanProgress(sessionID uint, scanned int, currentPath string) error {
	return r.db.Model(&model.ScanSession{}).Where("id = ?", sessionID).
		Updates(map[string]interface{}{"scanned_count": scanned, "current_path": currentPath}).Error
}

const ScanInterruptedMessage = "scan interrupted by server restart"

// The walk lives in memory, so a scan whose process died can never finish.
func (r *Repository) MarkInterruptedScans() (int64, error) {
	res := r.db.Model(&model.ScanSession{}).Where("status = ?", "scanning").
		Updates(map[string]interface{}{
			"status":        "interrupted",
			"error_message": ScanInterruptedMessage,
			"current_path":  "",
		})
	return res.RowsAffected, res.Error
}

// Never called on a read path: writers call it at the end of a phase, and
// entry writes schedule it through touchCounts.
func (r *Repository) RefreshSessionCounts(id uint) error { return r.refreshCounts(r.Reader(), id) }

func (r *Repository) refreshCounts(reader *gorm.DB, id uint) error {
	var c struct {
		Total    int64
		Tagged   int64
		Planned  int64
		Review   int64
		Executed int64
		Size     int64
	}
	if err := reader.Model(&model.FileEntry{}).Where("scan_session_id = ?", id).
		Select(`COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN tagged = ? THEN 1 ELSE 0 END), 0) AS tagged,
			COALESCE(SUM(CASE WHEN operation != '' THEN 1 ELSE 0 END), 0) AS planned,
			COALESCE(SUM(CASE WHEN operation != '' AND needs_review = ? AND executed = ? THEN 1 ELSE 0 END), 0) AS review,
			COALESCE(SUM(CASE WHEN executed = ? THEN 1 ELSE 0 END), 0) AS executed,
			COALESCE(SUM(size), 0) AS size`, true, true, false, true).
		Scan(&c).Error; err != nil {
		return err
	}
	// Column-scoped (never a Save of a loaded session, which would race the
	// scan/run writers' status updates); the WHERE makes an unchanged row a
	// no-op.
	res := r.db.Model(&model.ScanSession{}).
		Where("id = ? AND (total_files <> ? OR tagged_files <> ? OR planned_ops <> ? OR review_count <> ? OR executed_ops <> ? OR total_size <> ?)",
			id, c.Total, c.Tagged, c.Planned, c.Review, c.Executed, c.Size).
		Updates(map[string]any{
			"total_files":  c.Total,
			"tagged_files": c.Tagged,
			"planned_ops":  c.Planned,
			"review_count": c.Review,
			"executed_ops": c.Executed,
			"total_size":   c.Size,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		Emit(id, 0, EvSessionUpdated, struct{}{})
	}
	return nil
}

var countsDelay = time.Second

type countsKey struct {
	db *gorm.DB
	id uint
}

type pendingCount struct {
	timer *time.Timer
	run   func()
}

var counts = struct {
	mu      sync.Mutex
	pending map[countsKey]*pendingCount
	wg      sync.WaitGroup // one per armed timer, done when its refresh has run
}{pending: make(map[countsKey]*pendingCount)}

func (r *Repository) touchCounts(sessionID uint) {
	k := countsKey{r.db, sessionID}
	counts.mu.Lock()
	defer counts.mu.Unlock()
	if _, ok := counts.pending[k]; ok {
		return
	}
	reader := r.Reader() // resolved here: the timer must not read the package globals
	run := func() {
		if err := dal.WithRetry(func() error { return r.refreshCounts(reader, sessionID) }); err != nil {
			log.Printf("refresh counts of session %d: %v", sessionID, err)
		}
	}
	p := &pendingCount{run: run}
	counts.wg.Add(1)
	p.timer = time.AfterFunc(countsDelay, func() {
		defer counts.wg.Done()
		counts.mu.Lock()
		delete(counts.pending, k)
		counts.mu.Unlock()
		run()
	})
	counts.pending[k] = p
}

// FlushCounts runs every scheduled counter refresh now and waits for the ones
// already firing. Shutdown calls it before the database closes.
func FlushCounts() {
	counts.mu.Lock()
	armed := make([]*pendingCount, 0, len(counts.pending))
	for k, p := range counts.pending {
		armed = append(armed, p)
		delete(counts.pending, k)
	}
	counts.mu.Unlock()
	for _, p := range armed {
		if p.timer.Stop() {
			p.run()
			counts.wg.Done()
		}
	}
	counts.wg.Wait()
}

type SessionProgress struct {
	Total    int
	Tagged   int
	Planned  int
	Executed int
}

// Read-only, so concurrent workers can call it after every unit without
// risking a lost update on Status.
func (r *Repository) GetSessionProgress(id uint) (SessionProgress, error) {
	var counts struct {
		Total    int64
		Tagged   int64
		Planned  int64
		Executed int64
	}
	err := r.Reader().Model(&model.FileEntry{}).Where("scan_session_id = ?", id).
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

func (r *Repository) GetFile(id uint) (*model.FileEntry, error) {
	var f model.FileEntry
	err := r.db.First(&f, id).Error
	return &f, err
}

func (r *Repository) GetFileByPath(sessionID uint, path string) (*model.FileEntry, error) {
	var f model.FileEntry
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

// id makes the order total so pages never overlap.
const fileOrder = "file_type ASC, name ASC, id ASC"

// The REST listing, whose UI shows page numbers; iterating callers use
// ListFilesAfter.
func (r *Repository) ListFiles(q FileQuery) ([]model.FileEntry, int64, error) {
	tx := applyFileFilters(r.db.Model(&model.FileEntry{}), q)

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

	var files []model.FileEntry
	offset := (q.Page - 1) * q.PageSize
	err := tx.Order(fileOrder).Offset(offset).Limit(q.PageSize).Find(&files).Error
	return files, total, err
}

var ErrBadCursor = errors.New("bad cursor")

// For a directory listing idx_session_parent serves both the filter and the
// order, so every page costs the same.
func (r *Repository) ListFilesAfter(q FileQuery, afterID uint, limit int) ([]model.FileEntry, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	tx := applyFileFilters(r.db.Model(&model.FileEntry{}), q)
	if afterID > 0 {
		var last []model.FileEntry
		if err := r.db.Select("id", "file_type", "name").Where("scan_session_id = ? AND id = ?", q.SessionID, afterID).Limit(1).Find(&last).Error; err != nil {
			return nil, false, err
		}
		if len(last) == 0 {
			return nil, false, ErrBadCursor
		}
		tx = tx.Where("(file_type, name, id) > (?, ?, ?)", last[0].FileType, last[0].Name, last[0].ID)
	}
	var files []model.FileEntry
	if err := tx.Order(fileOrder).Limit(limit + 1).Find(&files).Error; err != nil {
		return nil, false, err
	}
	if len(files) > limit {
		return files[:limit], true, nil
	}
	return files, false, nil
}

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
		tx = tx.Where(nameSearchCondition(tx), "%"+dal.LikeLiteral(q.Search)+"%")
	}
	return tx
}

// Probes random points of the matching id range (one indexed "id >= x LIMIT
// 1" each) instead of loading every id; rows after id gaps are slightly
// favored.
func (r *Repository) SampleFiles(q FileQuery, n int) ([]model.FileEntry, error) {
	if n <= 0 {
		return nil, nil
	}
	base := func() *gorm.DB { return applyFileFilters(r.db.Model(&model.FileEntry{}), q) }
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
	var files []model.FileEntry
	if len(ids) == 0 {
		return files, nil
	}
	err := r.db.Where("id IN ?", ids).Order("original_path ASC").Find(&files).Error
	return files, err
}

// eino executes parallel tool calls, so a full-row Save of a loaded struct
// would clobber concurrent field writes.
func (r *Repository) UpdateDescription(sessionID uint, path, description string) error {
	return r.db.Model(&model.FileEntry{}).
		Where("scan_session_id = ? AND original_path = ?", sessionID, path).
		Update("description", description).Error
}

func (r *Repository) TaggedStatusByIDs(ids []uint) (map[uint]bool, error) {
	out := make(map[uint]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.FileEntry
	if err := r.db.Select("id", "tagged").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, f := range rows {
		out[f.ID] = f.Tagged
	}
	return out, nil
}

// Untagged directories may be containers deliberately left for their children
// and symlinks are never tagged; only files signal misses.
type UntaggedBreakdown struct {
	Directories int64
	Files       int64
	Symlinks    int64
}

func (b UntaggedBreakdown) Total() int64 {
	return b.Directories + b.Files + b.Symlinks
}

func (r *Repository) CountUntaggedByType(sessionID uint) (UntaggedBreakdown, error) {
	var b UntaggedBreakdown
	err := r.db.Model(&model.FileEntry{}).
		Where("scan_session_id = ? AND tagged = ?", sessionID, false).
		Select(`SUM(CASE WHEN file_type = 'directory' THEN 1 ELSE 0 END) AS directories,
			SUM(CASE WHEN file_type = 'file' THEN 1 ELSE 0 END) AS files,
			SUM(CASE WHEN file_type = 'symlink' THEN 1 ELSE 0 END) AS symlinks`).
		Scan(&b).Error
	return b, err
}

// Junk is decided as skip through DecideBatch with override, so decided entries
// below a junk folder are reported by the decision rules and never overwritten
// silently. Entries under a unit or skipped folder are refused and stay as they
// are. Returns the number of entries marked, members included.
func (r *Repository) MarkJunkEntries(sessionID uint, fileNames, dirNames []string) (int64, error) {
	var files, dirs []model.FileEntry
	if err := r.db.Select("id", "original_path").
		Where("scan_session_id = ? AND role = ? AND file_type = 'file' AND (lower(name) IN ? OR name LIKE '.!_%'"+dal.LikeEscapeClause+")", sessionID, RoleUndecided, fileNames).
		Find(&files).Error; err != nil {
		return 0, err
	}
	if err := r.db.Select("id", "original_path").
		Where("scan_session_id = ? AND role = ? AND file_type = 'directory' AND lower(name) IN ?", sessionID, RoleUndecided, dirNames).
		Find(&dirs).Error; err != nil {
		return 0, err
	}
	ds := make([]Decision, 0, len(files)+len(dirs))
	for _, f := range files {
		ds = append(ds, Decision{Path: f.OriginalPath, Role: RoleSkip, Reason: "系统垃圾文件", Override: true})
	}
	for _, d := range dirs {
		ds = append(ds, Decision{Path: d.OriginalPath, Role: RoleSkip, Reason: "系统垃圾目录", Override: true})
	}
	outcomes, err := r.DecideBatch(sessionID, ds)
	if err != nil {
		return 0, err
	}
	var total int64
	var ids []uint
	for i, o := range outcomes {
		if o.Err != nil || o.Result == nil {
			continue
		}
		total += 1 + o.Result.Members
		if i < len(files) {
			ids = append(ids, files[i].ID)
			continue
		}
		d := dirs[i-len(files)]
		ids = append(ids, d.ID)
		// batch_index is bookkeeping, not part of the decision.
		if err := entriesUnder(r.db, sessionID, d.OriginalPath).Where("role = ?", RoleMember).
			Update("batch_index", JunkBatchIndex).Error; err != nil {
			return total, err
		}
	}
	for start := 0; start < len(ids); start += 500 {
		if err := r.db.Model(&model.FileEntry{}).Where("id IN ?", ids[start:min(start+500, len(ids))]).
			Update("batch_index", JunkBatchIndex).Error; err != nil {
			return total, err
		}
	}
	return total, nil
}

func (r *Repository) CountIgnoredJunk(sessionID uint) (int64, error) {
	var n int64
	err := r.db.Model(&model.FileEntry{}).
		Where("scan_session_id = ? AND batch_index = ?", sessionID, JunkBatchIndex).
		Count(&n).Error
	return n, err
}

// model.Filesystem-wide: a category filled by an earlier session shows zero in the
// current one yet is still in use.
func (r *Repository) CountPlannedByTargetPath(filesystemID uint, categoryPath string) (int64, error) {
	var n int64
	cond, args := targetCond(r.db, categoryPath)
	err := r.db.Model(&model.FileEntry{}).
		Where("scan_session_id IN (SELECT id FROM scan_sessions WHERE filesystem_id = ?) AND operation != ''", filesystemID).
		Where(cond, args...).
		Count(&n).Error
	return n, err
}

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
	return dal.WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			cond, args := targetCond(tx, categoryPath)
			var units []model.FileEntry
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
			return tx.Model(&model.FileEntry{}).Where("scan_session_id IN ?", sessions).Where(cond, args...).
				Updates(undecidedFields()).Error
		})
	})
}

func (r *Repository) filesystemSessionIDs(filesystemID uint) ([]uint, error) {
	var ids []uint
	err := r.db.Model(&model.ScanSession{}).Where("filesystem_id = ?", filesystemID).Pluck("id", &ids).Error
	return ids, err
}

func categoryDir(p string) string {
	return strings.TrimSuffix(NormalizeTargetPath(p), "/")
}

// Sibling prefixes ("/photo" vs "/photos2") never match.
func targetCond(tx *gorm.DB, categoryPath string) (string, []any) {
	dir := categoryDir(categoryPath)
	if dir == "" {
		return dal.SubtreeCond(tx, "new_path", "")
	}
	return dal.AtOrUnderCond(tx, "new_path", dir)
}

func (r *Repository) CreateCategoryWithConflict(c *model.Category) (created bool, err error) {
	err = r.db.Create(c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *Repository) ListCategories(filesystemID uint) ([]model.Category, error) {
	var cats []model.Category
	err := r.db.Where("filesystem_id = ?", filesystemID).Order("name ASC").Find(&cats).Error
	return cats, err
}

func (r *Repository) GetCategoryByName(filesystemID uint, name string) (*model.Category, error) {
	var cat model.Category
	err := r.db.Where("filesystem_id = ? AND name = ?", filesystemID, name).First(&cat).Error
	return &cat, err
}

type CategoryUsage struct {
	Name        string
	Path        string
	Description string
	Structure   string
	FileCount   int64
}

// Categories without planned files are included with a zero count so the
// prompt always shows the complete category system.
func (r *Repository) GetCategoryUsageSummary(filesystemID uint, sessionID uint) ([]CategoryUsage, error) {
	cats, err := r.ListCategories(filesystemID)
	if err != nil {
		return nil, err
	}
	stats, err := r.CategoryStats(sessionID, cats)
	if err != nil {
		return nil, err
	}
	result := make([]CategoryUsage, len(cats))
	for i, c := range cats {
		result[i] = CategoryUsage{Name: c.Name, Path: c.Path, Description: c.Description, Structure: c.Structure, FileCount: stats[i].Count}
	}
	return result, nil
}

// CategoryStat is the file count and total size under the category at Idx.
type CategoryStat struct {
	Idx   int
	Count int64
	Size  int64
}

// SQLite caps compound selects at 500.
const categoryStatsChunk = 200

// Every UNION ALL arm is an index range on idx_session_newpath.
func (r *Repository) CategoryStats(sessionID uint, cats []model.Category) ([]CategoryStat, error) {
	out := make([]CategoryStat, len(cats))
	for start := 0; start < len(cats); start += categoryStatsChunk {
		end := min(start+categoryStatsChunk, len(cats))
		arms := make([]string, 0, end-start)
		var args []any
		for i := start; i < end; i++ {
			cond, cargs := targetCond(r.db, cats[i].Path)
			arms = append(arms, fmt.Sprintf("SELECT %d AS idx, COUNT(*) AS count, COALESCE(SUM(size), 0) AS size FROM file_entries WHERE scan_session_id = ? AND %s", i, cond))
			args = append(append(args, sessionID), cargs...)
		}
		var rows []CategoryStat
		if err := r.db.Raw(strings.Join(arms, " UNION ALL "), args...).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[row.Idx] = row
		}
	}
	return out, nil
}

// UpdateCategoryPath writes the category's editable columns and cascades a path
// change into the planned targets of the category's own filesystem, in one
// transaction. filesystem_id is never written. The prefix is swapped by
// position, so "/photo" -> "/pics" leaves "/photos2" alone and a repeated
// substring deeper in the path is untouched.
func (r *Repository) UpdateCategoryPath(c *model.Category, oldPath string) error {
	from, to := categoryDir(oldPath), categoryDir(c.Path)
	return dal.WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model.Category{}).Where("id = ?", c.ID).Updates(map[string]any{
				"name": c.Name, "path": c.Path, "structure": c.Structure, "description": c.Description,
				"updated_at": time.Now(),
			}).Error; err != nil {
				return err
			}
			if from == "" || to == "" || from == to {
				return nil
			}
			var sessions []uint
			if err := tx.Model(&model.ScanSession{}).Where("filesystem_id = ?", c.FilesystemID).Pluck("id", &sessions).Error; err != nil {
				return err
			}
			if len(sessions) == 0 {
				return nil
			}
			cond, args := dal.AtOrUnderCond(tx, "new_path", from)
			return tx.Model(&model.FileEntry{}).Where("scan_session_id IN ?", sessions).Where(cond, args...).
				Updates(map[string]any{
					"new_path":     gorm.Expr(dal.ConcatExpr(tx, "?", "substr(new_path, ?)"), to, utf8.RuneCountInString(from)+1),
					"new_path_key": gorm.Expr(dal.ConcatExpr(tx, "?", "substr(new_path_key, ?)"), CollisionKey(to), utf8.RuneCountInString(CollisionKey(from))+1),
				}).Error
		})
	})
}

func (r *Repository) DeleteCategory(id uint) error {
	return r.db.Delete(&model.Category{}, id).Error
}

func (r *Repository) GetModelProvider(id uint) (*model.ModelProvider, error) {
	var m model.ModelProvider
	err := r.db.First(&m, id).Error
	return &m, err
}

func (r *Repository) NextManualBatchIndex(sessionID uint) int {
	var minBatch *int
	r.db.Model(&model.FileEntry{}).
		Where("scan_session_id = ? AND batch_index < ?", sessionID, JunkBatchIndex).
		Select("MIN(batch_index)").Scan(&minBatch)
	if minBatch == nil {
		return JunkBatchIndex - 1
	}
	return *minBatch - 1
}

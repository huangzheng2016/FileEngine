package db

import (
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

	var total, tagged, planned, executed int64
	r.db.Model(&FileEntry{}).Where("scan_session_id = ?", id).Count(&total)
	r.db.Model(&FileEntry{}).Where("scan_session_id = ? AND tagged = ?", id, true).Count(&tagged)
	r.db.Model(&FileEntry{}).Where("scan_session_id = ? AND operation != ''", id).Count(&planned)
	r.db.Model(&FileEntry{}).Where("scan_session_id = ? AND executed = ?", id, true).Count(&executed)

	session.TotalFiles = int(total)
	session.TaggedFiles = int(tagged)
	session.PlannedOps = int(planned)
	session.ExecutedOps = int(executed)
	return r.db.Save(&session).Error
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
}

func (r *Repository) ListFiles(q FileQuery) ([]FileEntry, int64, error) {
	tx := r.db.Model(&FileEntry{})
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
		tx = tx.Where("new_path LIKE ?", *q.CategoryPath+"%")
	}
	if q.Search != "" {
		tx = tx.Where("name LIKE ?", "%"+q.Search+"%")
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

	var files []FileEntry
	err := tx.Order("file_type ASC, name ASC").Offset(offset).Limit(q.PageSize).Find(&files).Error
	return files, total, err
}

func (r *Repository) UpdateFile(f *FileEntry) error {
	return r.db.Save(f).Error
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

func (r *Repository) MarkChildrenTagged(sessionID uint, parentPath string) error {
	return r.db.Model(&FileEntry{}).
		Where("scan_session_id = ? AND original_path LIKE ?", sessionID, parentPath+"/%").
		Update("tagged", true).Error
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

func (r *Repository) UpdateCategory(c *Category) error {
	return r.db.Save(c).Error
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

// ============ AgentLog ============

func (r *Repository) CreateAgentLog(log *AgentLog) error {
	return r.db.Create(log).Error
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

func (r *Repository) ListBatches(sessionID uint, page, pageSize int) ([]int, int64, error) {
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

	var batches []int
	err := r.db.Model(&AgentLog{}).
		Where("scan_session_id = ?", sessionID).
		Distinct("batch_index").
		Order("batch_index ASC").
		Offset(offset).Limit(pageSize).
		Pluck("batch_index", &batches).Error
	return batches, total, err
}

package db

import (
	"path"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"gorm.io/gorm"
)

// Preview statuses of a source-side entry.
const (
	PreviewMoved   = "moved"
	PreviewKept    = "kept"
	PreviewPartial = "partial"
)

// PreviewItem is one child of a preview directory. After side: Path is the
// virtual target path; FileID/OriginalPath are set for a single planned entry.
// Before side: Path is the original path, Status says what happens to it.
type PreviewItem struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	IsDir        bool   `json:"is_dir"`
	FileCount    int64  `json:"file_count"`
	Size         int64  `json:"size"`
	FileID       uint   `json:"file_id,omitempty"`
	OriginalPath string `json:"original_path,omitempty"`
	Status       string `json:"status,omitempty"`
	NewPath      string `json:"new_path,omitempty"`
	MovedCount   int64  `json:"moved_count,omitempty"`
}

// PreviewPage is one page of a preview directory listing.
type PreviewPage struct {
	Items []PreviewItem `json:"items"`
	Total int64         `json:"total"`
}

// dirStat is the recursive file count and size under a directory.
type dirStat struct{ files, size int64 }

// dirStatsCache memoizes recursive directory stats per session. The scanned
// tree is immutable between scans, so the stamp (entry count + max id)
// changes exactly when a rescan rewrites the rows.
var dirStatsCache = struct {
	sync.Mutex
	m map[uint]dirStatsEntry
}{m: map[uint]dirStatsEntry{}}

type dirStatsEntry struct {
	count, maxID int64
	stats        map[string]dirStat
}

// sessionDirStats returns recursive (files, size) per directory path of the
// session, computed from one GROUP BY parent_path query and rolled up to the
// ancestors in Go.
func (r *Repository) sessionDirStats(sessionID uint) (map[string]dirStat, error) {
	var stamp struct{ Cnt, MaxID int64 }
	if err := r.db.Model(&FileEntry{}).Where("scan_session_id = ?", sessionID).
		Select("COUNT(*) AS cnt, COALESCE(MAX(id), 0) AS max_id").Scan(&stamp).Error; err != nil {
		return nil, err
	}
	dirStatsCache.Lock()
	cached, ok := dirStatsCache.m[sessionID]
	dirStatsCache.Unlock()
	if ok && cached.count == stamp.Cnt && cached.maxID == stamp.MaxID {
		return cached.stats, nil
	}

	var rows []struct {
		ParentPath string
		Cnt        int64
		Size       int64
	}
	if err := r.db.Model(&FileEntry{}).
		Where("scan_session_id = ? AND file_type = 'file'", sessionID).
		Select("parent_path, COUNT(*) AS cnt, COALESCE(SUM(size), 0) AS size").
		Group("parent_path").Scan(&rows).Error; err != nil {
		return nil, err
	}
	stats := make(map[string]dirStat, len(rows)*2)
	for _, row := range rows {
		p := row.ParentPath
		for {
			s := stats[p]
			s.files += row.Cnt
			s.size += row.Size
			stats[p] = s
			if p == "" || p == "." || p == "/" {
				break
			}
			next := path.Dir(p)
			if next == p {
				break
			}
			p = next
		}
	}
	dirStatsCache.Lock()
	dirStatsCache.m[sessionID] = dirStatsEntry{count: stamp.Cnt, maxID: stamp.MaxID, stats: stats}
	dirStatsCache.Unlock()
	return stats, nil
}

// pathAncestors returns p and its ancestors, nearest first ("a/b/c" →
// [a/b/c, a/b, a]; "/x/y" → [/x/y, /x]).
func pathAncestors(p string) []string {
	var out []string
	for p != "" && p != "." && p != "/" {
		out = append(out, p)
		next := path.Dir(p)
		if next == p {
			break
		}
		p = next
	}
	return out
}

// segmentGroup is one aggregated next-segment bucket below a prefix.
type segmentGroup struct {
	Seg    string
	Cnt    int64
	Files  int64
	Size   int64
	Nested int64
	MinID  uint
}

// segmentQuery groups the session's planned rows whose column value lies
// under prefix (which must end in "/", or be "" for "everything") by the
// next path segment below it — the aggregation runs in SQL, so a 100k-entry
// session costs one grouped scan per listing.
func (r *Repository) segmentQuery(sessionID uint, column, prefix string, extra string) *gorm.DB {
	tx := r.db
	instr := instrFunc(tx)
	start := utf8.RuneCountInString(prefix) + 1
	rest := "substr(" + column + ", ?)"
	seg := "CASE WHEN " + instr + "(" + rest + ", '/') > 0 THEN substr(" + rest + ", 1, " + instr + "(" + rest + ", '/') - 1) ELSE " + rest + " END"
	nested := "CASE WHEN " + instr + "(" + rest + ", '/') > 0 THEN 1 ELSE 0 END"
	where := "scan_session_id = ? AND operation != ''" + extra
	args := []any{start, start, start, start, start, sessionID}
	if prefix != "" {
		where += " AND " + column + " LIKE ?" + likeEscapeClause
		args = append(args, likeLiteral(prefix)+"%")
	}
	inner := tx.Raw("SELECT id, size, file_type, "+seg+" AS seg, "+nested+" AS nested FROM file_entries WHERE "+where, args...)
	return tx.Table("(?) AS t", inner)
}

// PreviewAfter lists the children of a virtual target directory (dir is
// "/"-rooted; "" or "/" is the target root). Directories planned as a whole
// stay browsable: inside one, the listing maps back to its real children.
func (r *Repository) PreviewAfter(sessionID uint, dir string, offset, limit int) (*PreviewPage, error) {
	dir = NormalizeTargetPath(dir)
	if dir == "/" {
		dir = ""
	}
	if limit <= 0 {
		limit = 200
	}
	stats, err := r.sessionDirStats(sessionID)
	if err != nil {
		return nil, err
	}

	// Inside a directory that moves as a whole?
	if anc := pathAncestors(dir); len(anc) > 0 {
		var moved []FileEntry
		if err := r.db.Where("scan_session_id = ? AND file_type = 'directory' AND operation != '' AND new_path IN ?", sessionID, anc).
			Find(&moved).Error; err != nil {
			return nil, err
		}
		if len(moved) > 0 {
			sort.Slice(moved, func(i, j int) bool { return len(moved[i].NewPath) > len(moved[j].NewPath) })
			d := moved[0]
			orig := d.OriginalPath + strings.TrimPrefix(dir, d.NewPath)
			return r.previewRealChildren(sessionID, orig, dir, stats, offset, limit)
		}
	}

	prefix := dir + "/"
	base := r.segmentQuery(sessionID, "new_path", prefix, "")
	var total int64
	if err := r.db.Table("(?) AS g", base.Select("seg").Group("seg")).Count(&total).Error; err != nil {
		return nil, err
	}
	var groups []segmentGroup
	if err := r.segmentQuery(sessionID, "new_path", prefix, "").
		Select(`seg, COUNT(*) AS cnt,
			SUM(CASE WHEN file_type = 'file' THEN 1 ELSE 0 END) AS files,
			COALESCE(SUM(CASE WHEN file_type = 'file' THEN size ELSE 0 END), 0) AS size,
			MAX(nested) AS nested, MIN(id) AS min_id`).
		Group("seg").Order("seg ASC").Offset(offset).Limit(limit).Scan(&groups).Error; err != nil {
		return nil, err
	}

	// Whole-directory targets inside these groups contribute their scanned
	// subtree's files.
	segs := make([]string, len(groups))
	for i, g := range groups {
		segs[i] = g.Seg
	}
	var plannedDirs []FileEntry
	if len(groups) > 0 {
		if err := r.db.Select("id", "original_path", "new_path").
			Where("scan_session_id = ? AND file_type = 'directory' AND operation != '' AND new_path LIKE ?"+likeEscapeClause, sessionID, likeLiteral(prefix)+"%").
			Find(&plannedDirs).Error; err != nil {
			return nil, err
		}
	}
	extra := map[string]dirStat{}
	for _, d := range plannedDirs {
		seg, _, _ := strings.Cut(strings.TrimPrefix(d.NewPath, prefix), "/")
		s := stats[d.OriginalPath]
		e := extra[seg]
		e.files += s.files
		e.size += s.size
		extra[seg] = e
	}

	// Leaves (exactly one entry targeting dir/seg) carry the entry itself.
	var leafIDs []uint
	for _, g := range groups {
		if g.Nested == 0 && g.Cnt == 1 {
			leafIDs = append(leafIDs, g.MinID)
		}
	}
	leaves := map[uint]FileEntry{}
	if len(leafIDs) > 0 {
		var rows []FileEntry
		if err := r.db.Select("id", "original_path", "file_type", "size").Where("id IN ?", leafIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, f := range rows {
			leaves[f.ID] = f
		}
	}

	page := &PreviewPage{Items: make([]PreviewItem, 0, len(groups)), Total: total}
	for _, g := range groups {
		item := PreviewItem{Name: g.Seg, Path: prefix + g.Seg, FileCount: g.Files + extra[g.Seg].files, Size: g.Size + extra[g.Seg].size}
		if leaf, ok := leaves[g.MinID]; ok && g.Nested == 0 && g.Cnt == 1 {
			item.FileID = leaf.ID
			item.OriginalPath = leaf.OriginalPath
			item.IsDir = leaf.FileType == "directory"
		} else {
			item.IsDir = true
		}
		page.Items = append(page.Items, item)
	}
	sortPreviewItems(page.Items)
	return page, nil
}

// previewRealChildren lists the scanned children of orig as they appear at
// virtualDir (inside a directory that moves as a whole).
func (r *Repository) previewRealChildren(sessionID uint, orig, virtualDir string, stats map[string]dirStat, offset, limit int) (*PreviewPage, error) {
	tx := r.db.Model(&FileEntry{}).Where("scan_session_id = ? AND parent_path = ?", sessionID, orig)
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []FileEntry
	if err := tx.Order("file_type ASC, name ASC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	page := &PreviewPage{Items: make([]PreviewItem, 0, len(rows)), Total: total}
	for _, f := range rows {
		item := PreviewItem{Name: f.Name, Path: virtualDir + "/" + f.Name, IsDir: f.FileType == "directory", FileID: f.ID, OriginalPath: f.OriginalPath}
		if item.IsDir {
			s := stats[f.OriginalPath]
			item.FileCount, item.Size = s.files, s.size
		} else {
			item.FileCount, item.Size = 1, f.Size
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

// PreviewBefore lists the scanned children of an original directory (dir ""
// means the session root) with what happens to each: moved (its own or an
// ancestor's target), partial (a directory with some planned descendants) or
// kept.
func (r *Repository) PreviewBefore(sessionID uint, rootPath, dir string, offset, limit int) (*PreviewPage, error) {
	if dir == "" || dir == "." || dir == "/" {
		dir = rootPath
	}
	if limit <= 0 {
		limit = 200
	}
	stats, err := r.sessionDirStats(sessionID)
	if err != nil {
		return nil, err
	}

	// An ancestor (or dir itself) moving as a whole moves every child.
	var movedAnc *FileEntry
	if anc := pathAncestors(dir); len(anc) > 0 && dir != rootPath {
		var rows []FileEntry
		if err := r.db.Where("scan_session_id = ? AND operation != '' AND original_path IN ?", sessionID, anc).Find(&rows).Error; err != nil {
			return nil, err
		}
		for i := range rows {
			if movedAnc == nil || len(rows[i].OriginalPath) > len(movedAnc.OriginalPath) {
				movedAnc = &rows[i]
			}
		}
	}

	// Planned descendants per child, aggregated by next segment in SQL.
	movedBelow := map[string]int64{}
	if movedAnc == nil {
		prefix := dir + "/"
		if dir == "" {
			prefix = ""
		}
		var groups []segmentGroup
		if err := r.segmentQuery(sessionID, "original_path", prefix, "").
			Select("seg, MAX(nested) AS nested, SUM(nested) AS cnt").
			Group("seg").Scan(&groups).Error; err != nil {
			return nil, err
		}
		for _, g := range groups {
			movedBelow[g.Seg] = g.Cnt
		}
	}

	tx := r.db.Model(&FileEntry{}).Where("scan_session_id = ? AND parent_path = ?", sessionID, dir)
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []FileEntry
	if err := tx.Order("file_type ASC, name ASC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	page := &PreviewPage{Items: make([]PreviewItem, 0, len(rows)), Total: total}
	for _, f := range rows {
		item := PreviewItem{Name: f.Name, Path: f.OriginalPath, IsDir: f.FileType == "directory", FileID: f.ID, OriginalPath: f.OriginalPath}
		if item.IsDir {
			s := stats[f.OriginalPath]
			item.FileCount, item.Size = s.files, s.size
		} else {
			item.FileCount, item.Size = 1, f.Size
		}
		switch {
		case movedAnc != nil:
			item.Status = PreviewMoved
			item.NewPath = movedAnc.NewPath + strings.TrimPrefix(f.OriginalPath, movedAnc.OriginalPath)
		case f.Operation != "":
			item.Status = PreviewMoved
			item.NewPath = f.NewPath
		case item.IsDir && movedBelow[f.Name] > 0:
			item.Status = PreviewPartial
			item.MovedCount = movedBelow[f.Name]
		default:
			item.Status = PreviewKept
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

// sortPreviewItems orders directories first, then by name.
func sortPreviewItems(items []PreviewItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return items[i].Name < items[j].Name
	})
}

// GetEntriesByPaths loads the session's entries with the given original
// paths (used for parent-chain descriptions in prompts).
func (r *Repository) GetEntriesByPaths(sessionID uint, paths []string) ([]FileEntry, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	var rows []FileEntry
	err := r.db.Where("scan_session_id = ? AND original_path IN ?", sessionID, paths).Find(&rows).Error
	return rows, err
}

package scanner

import (
	"sync/atomic"

	"FileEngine/internal/db"
)

// indexEntry is an existing row of the session, loaded before a rescan.
type indexEntry struct {
	id       uint
	size     int64
	mtime    int64 // unix seconds: dialects store sub-second precision differently
	fileType string
	children int
	subdirs  int
	seen     atomic.Bool // set by the walk; unseen rows are deleted afterwards
}

// scanIndex maps original_path to the existing row; nil on a first scan. The
// map is read-only during the walk (only the atomic seen flags change), so
// walk workers share it without locking.
type scanIndex map[string]*indexEntry

func (s *Scanner) loadIndex(sessionID uint) (scanIndex, error) {
	idx := scanIndex{}
	err := s.repo.ForEachScanIndexRow(sessionID, func(r db.ScanIndexRow) {
		idx[r.OriginalPath] = &indexEntry{
			id: r.ID, size: r.Size, mtime: r.ModTime.Unix(), fileType: r.FileType,
			children: r.ChildCount, subdirs: r.SubdirCount,
		}
	})
	if err != nil || len(idx) == 0 {
		return nil, err
	}
	return idx, nil
}

// emit routes one walked row: insert when new; for an existing row nothing
// when unchanged, a count/mtime refresh for directories (their mtime moves
// with every child add/remove, and their decision is about the folder as a
// whole), and a full reset to undecided for changed files.
func (idx scanIndex) emit(b *writeBatch, fe db.FileEntry) {
	ex := idx[fe.OriginalPath]
	if ex == nil {
		b.inserts = append(b.inserts, fe)
		return
	}
	ex.seen.Store(true)
	switch {
	case ex.fileType != fe.FileType:
		b.updates = append(b.updates, db.EntryUpdate{ID: ex.id, Cols: resetCols(fe)})
	case fe.FileType == "directory":
		if ex.children != fe.ChildCount || ex.subdirs != fe.SubdirCount || ex.mtime != fe.ModTime.Unix() {
			b.updates = append(b.updates, db.EntryUpdate{ID: ex.id, Cols: map[string]any{
				"child_count": fe.ChildCount, "subdir_count": fe.SubdirCount, "mod_time": fe.ModTime,
			}})
		}
	case ex.size != fe.Size || ex.mtime != fe.ModTime.Unix():
		b.updates = append(b.updates, db.EntryUpdate{ID: ex.id, Cols: resetCols(fe)})
	}
}

// resetCols rewrites a changed entry's scan columns and drops every decision
// and derived value: the content differs, so the old plan no longer applies.
func resetCols(fe db.FileEntry) map[string]any {
	return map[string]any{
		"size": fe.Size, "mod_time": fe.ModTime, "permissions": fe.Permissions, "file_type": fe.FileType,
		"child_count": fe.ChildCount, "subdir_count": fe.SubdirCount,
		"role": db.RoleUndecided, "reason": "", "new_path": "", "operation": "", "tagged": false,
		"executed": false, "executed_at": nil, "execute_mode": "", "description": "", "batch_index": 0,
		"taken_at": nil, "camera": "", "has_gps": false, "date_source": "",
		"hash": "", "duplicate_of": nil,
	}
}

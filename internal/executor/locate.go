package executor

import (
	"path"
	"time"

	"FileEngine/internal/db"
)

// LocateItem answers "where did my file go" for one entry.
type LocateItem struct {
	EntryID      uint       `json:"entry_id"`
	OriginalPath string     `json:"original_path"`
	Name         string     `json:"name"`
	FileType     string     `json:"file_type"`
	NewPath      string     `json:"new_path"`   // effective target; empty means it stays in place
	DecidedBy    string     `json:"decided_by"` // original path of the deciding entry (itself or an ancestor)
	Executed     bool       `json:"executed"`
	ExecutedAt   *time.Time `json:"executed_at"`
	ExecuteMode  string     `json:"execute_mode"`
	OpState      string     `json:"op_state"` // latest journal state of the deciding entry
}

// Locate finds entries by original path or name and resolves each one's
// effective target through the nearest decided ancestor-or-self.
func Locate(repo *db.Repository, sessionID uint, q string, limit int) ([]LocateItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	found, err := repo.SearchEntries(sessionID, q, limit)
	if err != nil {
		return nil, err
	}
	items := make([]LocateItem, len(found))
	deciderOf := make([]uint, len(found))
	var deciders []uint
	for i := range found {
		f := &found[i]
		items[i] = LocateItem{EntryID: f.ID, OriginalPath: f.OriginalPath, Name: f.Name, FileType: f.FileType}
		dec, err := decidingEntry(repo, sessionID, f)
		if err != nil {
			return nil, err
		}
		if dec == nil {
			continue
		}
		rel := cleanPath(f.OriginalPath)[len(cleanPath(dec.OriginalPath)):]
		items[i].NewPath = path.Join(cleanPath(dec.NewPath), rel)
		items[i].DecidedBy = dec.OriginalPath
		items[i].Executed, items[i].ExecutedAt, items[i].ExecuteMode = dec.Executed, dec.ExecutedAt, dec.ExecuteMode
		deciderOf[i] = dec.ID
		deciders = append(deciders, dec.ID)
	}
	ops, err := repo.LatestExecOpByEntry(deciders)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if op, ok := ops[deciderOf[i]]; ok && deciderOf[i] != 0 {
			items[i].OpState = op.State
		}
	}
	return items, nil
}

func decidingEntry(repo *db.Repository, sessionID uint, f *db.FileEntry) (*db.FileEntry, error) {
	if f.Operation != "" && f.NewPath != "" {
		return f, nil
	}
	var anc []string
	for p := parentOf(cleanPath(f.OriginalPath)); p != ""; p = parentOf(p) {
		anc = append(anc, p, "/"+p)
	}
	if len(anc) == 0 {
		return nil, nil
	}
	var rows []db.FileEntry
	if err := repo.DB().Where("scan_session_id = ? AND original_path IN ? AND operation != '' AND new_path != ''", sessionID, anc).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	var best *db.FileEntry
	for i := range rows {
		if best == nil || len(rows[i].OriginalPath) > len(best.OriginalPath) {
			best = &rows[i]
		}
	}
	return best, nil
}

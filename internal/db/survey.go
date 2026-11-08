package db

// EntryBrief is the slim projection of a FileEntry the survey and review
// passes stream over.
type EntryBrief struct {
	ID           uint
	OriginalPath string
	Name         string
	FileType     string
	Size         int64
	Depth        int
	NewPath      string
	Role         string
	Reason       string
}

// EachEntry streams every entry of the session in id order (all of them
// when plannedOnly is false, else only entries with a target that are not
// unit members). fn returning false stops the walk.
func (r *Repository) EachEntry(sessionID uint, plannedOnly bool, fn func(EntryBrief) bool) error {
	q := r.Reader().Model(&FileEntry{}).
		Select("id, original_path, name, file_type, size, depth, new_path, role, reason").
		Where("scan_session_id = ?", sessionID)
	if plannedOnly {
		q = q.Where("new_path <> '' AND role <> ?", RoleMember)
	}
	rows, err := q.Order("id ASC").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e EntryBrief
		if err := rows.Scan(&e.ID, &e.OriginalPath, &e.Name, &e.FileType, &e.Size, &e.Depth, &e.NewPath, &e.Role, &e.Reason); err != nil {
			return err
		}
		if !fn(e) {
			break
		}
	}
	return rows.Err()
}

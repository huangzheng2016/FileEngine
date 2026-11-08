package db

// ReasonUserEdit is the decision reason of a target the user set by hand.
const ReasonUserEdit = "用户修改"

// SetUserTarget applies the user's manual target edit of f: a target equal
// to the entry's own path means "keep in place" and clears the target.
// Returns the final target ("" when cleared) after collision resolution.
func (r *Repository) SetUserTarget(f *FileEntry, newPath string) (string, error) {
	newPath = NormalizeTargetPath(newPath)
	if newPath == NormalizeTargetPath(f.OriginalPath) {
		newPath = ""
	}
	final, _, err := r.SetTargetUnique(f.ScanSessionID, f.OriginalPath, newPath, f.FileType)
	if err != nil {
		return "", err
	}
	return final, r.UpdateFileFields(f.ID, map[string]any{"reason": ReasonUserEdit})
}

package db

const ReasonUserEdit = "用户修改"

// An empty target, or one equal to the entry's own path, keeps the entry in
// place.
func UserTargetDecision(f *FileEntry, newPath string) Decision {
	role := RoleFile
	if newPath == "" || NormalizeTargetPath(newPath) == NormalizeTargetPath(f.OriginalPath) {
		role, newPath = RoleUndecided, ""
	}
	return Decision{Path: f.OriginalPath, Role: role, NewPath: newPath, Reason: ReasonUserEdit, Override: true}
}

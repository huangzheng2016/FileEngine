package db

// ReasonUserEdit is the decision reason of a target the user set by hand.
const ReasonUserEdit = "用户修改"

// UserTargetDecision is the decision for the user's manual target edit of f.
// An empty target, or one equal to the entry's own path, keeps the entry in
// place: its decision is cleared.
func UserTargetDecision(f *FileEntry, newPath string) Decision {
	role := RoleFile
	if newPath == "" || NormalizeTargetPath(newPath) == NormalizeTargetPath(f.OriginalPath) {
		role, newPath = RoleUndecided, ""
	}
	return Decision{Path: f.OriginalPath, Role: role, NewPath: newPath, Reason: ReasonUserEdit, Override: true}
}

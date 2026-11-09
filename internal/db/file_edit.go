package db

const ReasonUserEdit = "用户修改"

// An empty target, or one equal to the entry's own path, keeps the entry in
// place as a decision of its own, so a later tagging run does not decide it
// again.
func UserTargetDecision(f *FileEntry, newPath string) Decision {
	role := RoleFile
	if newPath == "" || NormalizeTargetPath(newPath) == NormalizeTargetPath(f.OriginalPath) {
		role, newPath = RoleSkip, ""
	}
	return Decision{Path: f.OriginalPath, Role: role, NewPath: newPath, Reason: ReasonUserEdit, Override: true}
}

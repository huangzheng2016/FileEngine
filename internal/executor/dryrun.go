package executor

type PlanItem struct {
	FileID       uint   `json:"file_id"`
	OriginalPath string `json:"original_path"`
	NewPath      string `json:"new_path"`
	Operation    string `json:"operation"`
	Name         string `json:"name"`
	FileType     string `json:"file_type"`
}

func (e *Executor) DryRun(sessionID uint) ([]PlanItem, error) {
	files, err := e.repo.ListPlannedEntries(sessionID)
	if err != nil {
		return nil, err
	}
	items := make([]PlanItem, len(files))
	for i, f := range files {
		items[i] = PlanItem{FileID: f.ID, OriginalPath: f.OriginalPath, NewPath: f.NewPath,
			Operation: f.Operation, Name: f.Name, FileType: f.FileType}
	}
	return items, nil
}

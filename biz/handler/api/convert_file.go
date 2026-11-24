package api

import (
	"FileEngine/biz/dal/db/model"
	file "FileEngine/biz/model/api/file"
	"FileEngine/internal/db"
)

func toFileEntry(f *model.FileEntry) *file.FileEntry {
	var dup int64
	if f.DuplicateOf != nil {
		dup = int64(*f.DuplicateOf)
	}
	return &file.FileEntry{
		ID: int64(f.ID), SessionID: int64(f.ScanSessionID), OriginalPath: f.OriginalPath, NewPath: f.NewPath,
		Operation: f.Operation, Role: f.Role, Reason: f.Reason, Confidence: f.Confidence,
		NeedsReview: f.NeedsReview, Sensitive: f.Sensitive, Executed: f.Executed,
		ExecutedAt: unixPtr(f.ExecutedAt), ExecuteMode: f.ExecuteMode, Name: f.Name, Size: f.Size,
		ModTime: unix(f.ModTime), Permissions: f.Permissions, FileType: f.FileType, Description: f.Description,
		Tagged: f.Tagged, ParentPath: f.ParentPath, Depth: int32(f.Depth), ChildCount: int32(f.ChildCount),
		SubdirCount: int32(f.SubdirCount), BatchIndex: int32(f.BatchIndex), TakenAt: unixPtr(f.TakenAt),
		Camera: f.Camera, HasGps: f.HasGPS, DateSource: f.DateSource, Hash: f.Hash, DuplicateOf: dup,
		CreatedAt: unix(f.CreatedAt), UpdatedAt: unix(f.UpdatedAt),
	}
}

func toTreeNode(f *model.FileEntry) *file.TreeNode {
	return &file.TreeNode{
		ID: int64(f.ID), Label: f.Name, OriginalPath: f.OriginalPath, FileType: f.FileType,
		ChildCount: int32(f.ChildCount), Tagged: f.Tagged, IsLeaf: f.SubdirCount == 0,
	}
}

// reviewConfidence is the threshold the client shows next to low-confidence decisions.
const reviewConfidence = db.ReviewConfidence

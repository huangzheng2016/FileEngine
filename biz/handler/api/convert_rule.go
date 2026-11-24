package api

import (
	"FileEngine/biz/dal/db/model"
	rule "FileEngine/biz/model/api/rule"
)

func toPlanRule(r *model.PlanRule) *rule.PlanRule {
	return &rule.PlanRule{
		ID: int64(r.ID), SessionID: int64(r.SessionID), Position: int32(r.Position), Name: r.Name, Exts: r.Exts,
		NameRegex: r.NameRegex, PathPrefix: r.PathPrefix, FileType: r.FileType, MinSize: r.MinSize, MaxSize: r.MaxSize,
		Target: r.Target, Role: r.Role, Source: r.Source, Matched: int32(r.Matched),
		CreatedAt: unix(r.CreatedAt), UpdatedAt: unix(r.UpdatedAt),
	}
}

// intPtr widens an optional i32 request field.
func intPtr(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

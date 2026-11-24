package api

import (
	"FileEngine/biz/dal/db/model"
	plan "FileEngine/biz/model/api/plan"
)

func toSessionPlan(p *model.SessionPlan) *plan.SessionPlan {
	return &plan.SessionPlan{
		SessionID: int64(p.SessionID), Version: int32(p.Version), Status: p.Status, Content: p.Content,
		UpdatedAt: unix(p.UpdatedAt), ApprovedAt: unixPtr(p.ApprovedAt), ReviewConfidence: reviewConfidence,
	}
}

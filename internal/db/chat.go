package db

import (
	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"
)

func (r *Repository) AddChatTurn(t *model.ChatTurn) error {
	return dal.WithRetry(func() error { return r.db.Create(t).Error })
}

// The latest summary turn (nil if none) and every turn after the range it
// covers, oldest first; turns with ID >= beforeID are excluded (0 = no
// bound).
func (r *Repository) ChatHistory(sessionID, beforeID uint) (*model.ChatTurn, []model.ChatTurn, error) {
	var summaries []model.ChatTurn
	q := r.db.Where("session_id = ? AND role = ?", sessionID, ChatRoleSummary)
	if beforeID > 0 {
		q = q.Where("id < ?", beforeID)
	}
	if err := q.Order("id DESC").Limit(1).Find(&summaries).Error; err != nil {
		return nil, nil, err
	}
	var summary *model.ChatTurn
	var after uint
	if len(summaries) > 0 {
		summary, after = &summaries[0], summaries[0].CoversThrough
	}
	var turns []model.ChatTurn
	tq := r.db.Where("session_id = ? AND role IN ? AND id > ?", sessionID, []string{ChatRoleUser, ChatRoleAssistant}, after)
	if beforeID > 0 {
		tq = tq.Where("id < ?", beforeID)
	}
	if err := tq.Order("id ASC").Find(&turns).Error; err != nil {
		return nil, nil, err
	}
	return summary, turns, nil
}

// ChatTurnsCovering returns the session's turns of one role whose
// covers_through is among covers.
func (r *Repository) ChatTurnsCovering(sessionID uint, role string, covers []uint) ([]model.ChatTurn, error) {
	var rows []model.ChatTurn
	err := r.db.Where("session_id = ? AND role = ? AND covers_through IN ?", sessionID, role, covers).Find(&rows).Error
	return rows, err
}

// LatestChatTurnCovering is the newest turn of one role with the given
// covers_through, or nil.
func (r *Repository) LatestChatTurnCovering(sessionID uint, role string, covers uint) (*model.ChatTurn, error) {
	var rows []model.ChatTurn
	err := r.db.Where("session_id = ? AND role = ? AND covers_through = ?", sessionID, role, covers).Order("id DESC").Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

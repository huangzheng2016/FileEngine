package db

// AddChatTurn appends one turn to the session conversation.
func (r *Repository) AddChatTurn(t *ChatTurn) error {
	return WithRetry(func() error { return r.db.Create(t).Error })
}

// ListChatTurns returns the session's user and assistant turns, oldest first.
func (r *Repository) ListChatTurns(sessionID uint) ([]ChatTurn, error) {
	var turns []ChatTurn
	err := r.db.Where("session_id = ? AND role IN ?", sessionID, []string{ChatRoleUser, ChatRoleAssistant}).
		Order("id ASC").Find(&turns).Error
	return turns, err
}

// ChatHistory returns what the assistant prompt is built from: the latest
// summary turn (nil if none) and every user/assistant turn after the range it
// covers, oldest first. Turns with ID >= beforeID are excluded (0 = no bound).
func (r *Repository) ChatHistory(sessionID, beforeID uint) (*ChatTurn, []ChatTurn, error) {
	var summaries []ChatTurn
	q := r.db.Where("session_id = ? AND role = ?", sessionID, ChatRoleSummary)
	if beforeID > 0 {
		q = q.Where("id < ?", beforeID)
	}
	if err := q.Order("id DESC").Limit(1).Find(&summaries).Error; err != nil {
		return nil, nil, err
	}
	var summary *ChatTurn
	var after uint
	if len(summaries) > 0 {
		summary, after = &summaries[0], summaries[0].CoversThrough
	}
	var turns []ChatTurn
	tq := r.db.Where("session_id = ? AND role IN ? AND id > ?", sessionID, []string{ChatRoleUser, ChatRoleAssistant}, after)
	if beforeID > 0 {
		tq = tq.Where("id < ?", beforeID)
	}
	if err := tq.Order("id ASC").Find(&turns).Error; err != nil {
		return nil, nil, err
	}
	return summary, turns, nil
}

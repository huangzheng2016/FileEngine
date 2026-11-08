package db

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrPlanVersionConflict is returned when a plan write carries a stale version.
var ErrPlanVersionConflict = errors.New("方案已被其他人修改，请刷新后重试")

// ErrPlanNotFound is returned when an operation needs an existing plan.
var ErrPlanNotFound = errors.New("还没有整理方案")

// GetPlan returns the session's plan. A session without a plan yields a
// zero plan ({session_id, version: 0}) and no error.
func (r *Repository) GetPlan(sessionID uint) (*SessionPlan, error) {
	var p SessionPlan
	err := r.db.Where("session_id = ?", sessionID).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &SessionPlan{SessionID: sessionID}, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// HasPlan reports whether the session has a plan (draft or approved).
func (r *Repository) HasPlan(sessionID uint) (bool, error) {
	var n int64
	err := r.db.Model(&SessionPlan{}).Where("session_id = ?", sessionID).Count(&n).Error
	return n > 0, err
}

// SavePlan writes the plan content and bumps its version. expectedVersion is
// the version the writer based its edit on (0 for "no plan yet"); a mismatch
// returns ErrPlanVersionConflict. A negative expectedVersion skips the check
// (agent writes). status "" keeps the current status (draft for a new plan).
func (r *Repository) SavePlan(sessionID uint, content string, expectedVersion int, status string) (*SessionPlan, error) {
	var out SessionPlan
	err := WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			var cur SessionPlan
			err := tx.Where("session_id = ?", sessionID).Take(&cur).Error
			now := time.Now()
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if expectedVersion > 0 {
					return ErrPlanVersionConflict
				}
				if status == "" {
					status = PlanDraft
				}
				out = SessionPlan{SessionID: sessionID, Version: 1, Status: status, Content: content, UpdatedAt: now}
				if status == PlanApproved {
					out.ApprovedAt = &now
				}
				return tx.Create(&out).Error
			}
			if err != nil {
				return err
			}
			if expectedVersion >= 0 && cur.Version != expectedVersion {
				return ErrPlanVersionConflict
			}
			if status == "" {
				status = cur.Status
			}
			updates := map[string]any{"version": cur.Version + 1, "status": status, "content": content, "updated_at": now}
			if status == PlanApproved && cur.Status != PlanApproved {
				updates["approved_at"] = now
			}
			res := tx.Model(&SessionPlan{}).Where("session_id = ? AND version = ?", sessionID, cur.Version).Updates(updates)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return ErrPlanVersionConflict
			}
			return tx.Where("session_id = ?", sessionID).Take(&out).Error
		})
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ApprovePlan marks the session's plan approved. The version is unchanged:
// approval is a status change, not a content edit.
func (r *Repository) ApprovePlan(sessionID uint) (*SessionPlan, error) {
	now := time.Now()
	res := r.db.Model(&SessionPlan{}).Where("session_id = ?", sessionID).
		Updates(map[string]any{"status": PlanApproved, "approved_at": now})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrPlanNotFound
	}
	return r.GetPlan(sessionID)
}

// DeleteSessionBrainData removes the session's plan, chat turns and
// session-scoped memory (shared working notes). Called when the session is
// deleted.
func (r *Repository) DeleteSessionBrainData(sessionID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id = ?", sessionID).Delete(&SessionPlan{}).Error; err != nil {
			return err
		}
		if err := tx.Where("session_id = ?", sessionID).Delete(&ChatTurn{}).Error; err != nil {
			return err
		}
		return tx.Where("scope = ? AND scope_id = ?", MemoryScopeSession, sessionID).Delete(&MemoryItem{}).Error
	})
}

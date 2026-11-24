package db

import (
	"errors"
	"time"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"

	"gorm.io/gorm"
)

var ErrPlanVersionConflict = errors.New("方案已被其他人修改，请刷新后重试")

var ErrPlanNotFound = errors.New("还没有整理方案")

func (r *Repository) GetPlan(sessionID uint) (*model.SessionPlan, error) {
	var p model.SessionPlan
	err := r.db.Where("session_id = ?", sessionID).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &model.SessionPlan{SessionID: sessionID}, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *Repository) HasPlan(sessionID uint) (bool, error) {
	var n int64
	err := r.db.Model(&model.SessionPlan{}).Where("session_id = ?", sessionID).Count(&n).Error
	return n > 0, err
}

// A negative expectedVersion skips the version check (agent writes); status
// "" keeps the current status.
func (r *Repository) SavePlan(sessionID uint, content string, expectedVersion int, status string) (*model.SessionPlan, error) {
	var out model.SessionPlan
	err := dal.WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			var cur model.SessionPlan
			err := tx.Where("session_id = ?", sessionID).Take(&cur).Error
			now := time.Now()
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if expectedVersion > 0 {
					return ErrPlanVersionConflict
				}
				if status == "" {
					status = PlanDraft
				}
				out = model.SessionPlan{SessionID: sessionID, Version: 1, Status: status, Content: content, UpdatedAt: now}
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
			res := tx.Model(&model.SessionPlan{}).Where("session_id = ? AND version = ?", sessionID, cur.Version).Updates(updates)
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

// Approval is a status change, not a content edit, so the version is
// unchanged.
func (r *Repository) ApprovePlan(sessionID uint) (*model.SessionPlan, error) {
	now := time.Now()
	res := r.db.Model(&model.SessionPlan{}).Where("session_id = ?", sessionID).
		Updates(map[string]any{"status": PlanApproved, "approved_at": now})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrPlanNotFound
	}
	return r.GetPlan(sessionID)
}

func (r *Repository) DeleteSessionBrainData(sessionID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id = ?", sessionID).Delete(&model.SessionPlan{}).Error; err != nil {
			return err
		}
		if err := tx.Where("session_id = ?", sessionID).Delete(&model.ChatTurn{}).Error; err != nil {
			return err
		}
		return tx.Where("scope = ? AND scope_id = ?", MemoryScopeSession, sessionID).Delete(&model.MemoryItem{}).Error
	})
}

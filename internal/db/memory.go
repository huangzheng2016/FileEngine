package db

import (
	"errors"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"

	"gorm.io/gorm"
)

const MemoryContentMaxRunes = 300

var ErrMemoryNotFound = errors.New("记忆不存在")

type MemoryValidationError struct{ Msg string }

func (e *MemoryValidationError) Error() string { return e.Msg }

func invalidMemory(msg string) error { return &MemoryValidationError{Msg: msg} }

func NormalizeMemoryPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return strings.Trim(path.Clean("/"+p), "/")
}

// One live item exists per (scope, scope_id, key).
func NormalizeMemoryKey(k string) string {
	return strings.ToLower(strings.Join(strings.Fields(k), " "))
}

func ValidMemoryScope(s string) bool {
	return s == MemoryScopeSession || s == MemoryScopeFilesystem || s == MemoryScopeGlobal
}

func ValidMemoryKind(k string) bool {
	switch k {
	case MemoryPreference, MemoryConvention, MemoryCorrection, MemoryFact, MemoryNote:
		return true
	}
	return false
}

func ValidateMemoryItem(m *model.MemoryItem) error {
	if !ValidMemoryScope(m.Scope) {
		return invalidMemory("scope 必须是 session / filesystem / global 之一")
	}
	if m.Scope == MemoryScopeGlobal && m.ScopeID != 0 {
		return invalidMemory("global 记忆的 scope_id 必须为 0")
	}
	if m.Scope != MemoryScopeGlobal && m.ScopeID == 0 {
		return invalidMemory("session / filesystem 记忆需要 scope_id")
	}
	if !ValidMemoryKind(m.Kind) {
		return invalidMemory("kind 必须是 preference / convention / correction / fact / note 之一")
	}
	if m.Key == "" {
		return invalidMemory("key 不能为空")
	}
	if strings.TrimSpace(m.Content) == "" {
		return invalidMemory("content 不能为空")
	}
	if utf8.RuneCountInString(m.Content) > MemoryContentMaxRunes {
		return invalidMemory("content 不能超过 300 字")
	}
	return nil
}

// pinned only ever turns on.
func (r *Repository) UpsertMemory(m *model.MemoryItem) (bool, error) {
	m.Key = NormalizeMemoryKey(m.Key)
	m.Content = strings.TrimSpace(m.Content)
	m.Path = NormalizeMemoryPath(m.Path)
	if err := ValidateMemoryItem(m); err != nil {
		return false, err
	}
	created := false
	err := dal.WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			var cur model.MemoryItem
			err := tx.Where("scope = ? AND scope_id = ? AND "+dal.QuoteCol(tx, "key")+" = ?", m.Scope, m.ScopeID, m.Key).Take(&cur).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				created = true
				m.ID = 0
				return tx.Create(m).Error
			}
			if err != nil {
				return err
			}
			created = false
			updates := map[string]any{"kind": m.Kind, "content": m.Content, "path": m.Path, "source": m.Source, "updated_at": time.Now()}
			if m.Pinned {
				updates["pinned"] = true
			}
			if err := tx.Model(&model.MemoryItem{}).Where("id = ?", cur.ID).Updates(updates).Error; err != nil {
				return err
			}
			return tx.Take(m, cur.ID).Error
		})
	})
	return created, err
}

func (r *Repository) GetMemory(id uint) (*model.MemoryItem, error) {
	var m model.MemoryItem
	err := r.db.Take(&m, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMemoryNotFound
	}
	return &m, err
}

const memoryOrder = "pinned DESC, updated_at DESC, id DESC"

func (r *Repository) ListMemory(scope string, scopeID *uint) ([]model.MemoryItem, error) {
	tx := r.db.Model(&model.MemoryItem{})
	if scope != "" {
		tx = tx.Where("scope = ?", scope)
	}
	if scopeID != nil {
		tx = tx.Where("scope_id = ?", *scopeID)
	}
	var items []model.MemoryItem
	err := tx.Order(memoryOrder).Find(&items).Error
	return items, err
}

func (r *Repository) ListLongTermMemory(filesystemID uint) ([]model.MemoryItem, error) {
	var items []model.MemoryItem
	err := r.db.Where("(scope = ? AND scope_id = 0) OR (scope = ? AND scope_id = ?)",
		MemoryScopeGlobal, MemoryScopeFilesystem, filesystemID).
		Order(memoryOrder).Find(&items).Error
	return items, err
}

func (r *Repository) UpdateMemory(id uint, apply func(m *model.MemoryItem)) (*model.MemoryItem, error) {
	var out model.MemoryItem
	err := dal.WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Take(&out, id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrMemoryNotFound
				}
				return err
			}
			apply(&out)
			out.Key = NormalizeMemoryKey(out.Key)
			out.Content = strings.TrimSpace(out.Content)
			out.Path = NormalizeMemoryPath(out.Path)
			if err := ValidateMemoryItem(&out); err != nil {
				return err
			}
			var clash int64
			if err := tx.Model(&model.MemoryItem{}).Where("scope = ? AND scope_id = ? AND "+dal.QuoteCol(tx, "key")+" = ? AND id <> ?", out.Scope, out.ScopeID, out.Key, id).
				Count(&clash).Error; err != nil {
				return err
			}
			if clash > 0 {
				return invalidMemory("同一范围内已存在相同 key 的记忆")
			}
			out.UpdatedAt = time.Now()
			return tx.Model(&model.MemoryItem{}).Where("id = ?", id).Updates(map[string]any{
				"scope": out.Scope, "scope_id": out.ScopeID, "kind": out.Kind, "key": out.Key,
				"content": out.Content, "path": out.Path, "source": out.Source, "pinned": out.Pinned,
				"updated_at": out.UpdatedAt,
			}).Error
		})
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) DeleteMemory(id uint) error {
	res := r.db.Delete(&model.MemoryItem{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrMemoryNotFound
	}
	return nil
}

type MemoryScopeRef struct {
	Scope   string
	ScopeID uint
}

type MemorySearch struct {
	Query  string
	Scopes []MemoryScopeRef
	Limit  int
}

func (r *Repository) SearchMemory(q MemorySearch) ([]model.MemoryItem, error) {
	if len(q.Scopes) == 0 {
		return nil, nil
	}
	tx := r.db.Model(&model.MemoryItem{})
	scopeCond := r.db.Where("1 = 0")
	for _, s := range q.Scopes {
		scopeCond = scopeCond.Or("scope = ? AND scope_id = ?", s.Scope, s.ScopeID)
	}
	tx = tx.Where(scopeCond)
	for _, term := range strings.Fields(q.Query) {
		pat := "%" + dal.LikeLiteral(term) + "%"
		tx = tx.Where("("+dal.CILikeCondition(tx, dal.QuoteCol(tx, "key"))+" OR "+dal.CILikeCondition(tx, "content")+")", pat, pat)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	var items []model.MemoryItem
	err := tx.Order(memoryOrder).Limit(limit).Find(&items).Error
	return items, err
}

func (r *Repository) NotesSince(sessionID, afterID uint, limit int) ([]model.MemoryItem, error) {
	if limit <= 0 {
		limit = 50
	}
	var items []model.MemoryItem
	err := r.db.Where("scope = ? AND scope_id = ? AND id > ?", MemoryScopeSession, sessionID, afterID).
		Order("id ASC").Limit(limit).Find(&items).Error
	return items, err
}

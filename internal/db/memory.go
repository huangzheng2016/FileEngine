package db

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// MemoryContentMaxRunes caps one memory item's content.
const MemoryContentMaxRunes = 300

// ErrMemoryNotFound is returned when a memory id does not exist.
var ErrMemoryNotFound = errors.New("记忆不存在")

// MemoryValidationError is a rejected memory write (bad field, key clash) —
// the caller's input is wrong, the store is fine.
type MemoryValidationError struct{ Msg string }

func (e *MemoryValidationError) Error() string { return e.Msg }

func invalidMemory(msg string) error { return &MemoryValidationError{Msg: msg} }

// NormalizeMemoryKey canonicalizes a memory key: trimmed, inner whitespace
// collapsed, lower-cased. One live item exists per (scope, scope_id, key).
func NormalizeMemoryKey(k string) string {
	return strings.ToLower(strings.Join(strings.Fields(k), " "))
}

// ValidMemoryScope / ValidMemoryKind report whether the value is a known enum.
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

// ValidateMemoryItem checks the fields every writer must respect.
func ValidateMemoryItem(m *MemoryItem) error {
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

// UpsertMemory writes m by (scope, scope_id, key): an existing item is
// updated in place (kind, content, path, source; pinned only ever turns on;
// hits are kept), otherwise a new item is created. m is filled with the
// stored row. Reports whether a new item was created.
func (r *Repository) UpsertMemory(m *MemoryItem) (bool, error) {
	m.Key = NormalizeMemoryKey(m.Key)
	m.Content = strings.TrimSpace(m.Content)
	if err := ValidateMemoryItem(m); err != nil {
		return false, err
	}
	created := false
	err := WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			var cur MemoryItem
			err := tx.Where("scope = ? AND scope_id = ? AND "+quoteCol(tx, "key")+" = ?", m.Scope, m.ScopeID, m.Key).Take(&cur).Error
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
			if err := tx.Model(&MemoryItem{}).Where("id = ?", cur.ID).Updates(updates).Error; err != nil {
				return err
			}
			return tx.Take(m, cur.ID).Error
		})
	})
	return created, err
}

func (r *Repository) GetMemory(id uint) (*MemoryItem, error) {
	var m MemoryItem
	err := r.db.Take(&m, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMemoryNotFound
	}
	return &m, err
}

// memoryOrder ranks items for display and prompts: pinned first, then most
// used, then most recently touched.
const memoryOrder = "pinned DESC, hits DESC, updated_at DESC, id DESC"

// ListMemory lists items of one scope (scope "" = every scope); scopeID nil
// means every scope id.
func (r *Repository) ListMemory(scope string, scopeID *uint) ([]MemoryItem, error) {
	tx := r.db.Model(&MemoryItem{})
	if scope != "" {
		tx = tx.Where("scope = ?", scope)
	}
	if scopeID != nil {
		tx = tx.Where("scope_id = ?", *scopeID)
	}
	var items []MemoryItem
	err := tx.Order(memoryOrder).Find(&items).Error
	return items, err
}

// ListLongTermMemory returns the global items plus the filesystem's items —
// the candidates of the always-on prompt memory block.
func (r *Repository) ListLongTermMemory(filesystemID uint) ([]MemoryItem, error) {
	var items []MemoryItem
	err := r.db.Where("(scope = ? AND scope_id = 0) OR (scope = ? AND scope_id = ?)",
		MemoryScopeGlobal, MemoryScopeFilesystem, filesystemID).
		Order(memoryOrder).Find(&items).Error
	return items, err
}

// UpdateMemory writes the given fields of one item (column-scoped). key is
// normalized; the result is re-validated as a whole.
func (r *Repository) UpdateMemory(id uint, apply func(m *MemoryItem)) (*MemoryItem, error) {
	var out MemoryItem
	err := WithRetry(func() error {
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
			if err := ValidateMemoryItem(&out); err != nil {
				return err
			}
			var clash int64
			if err := tx.Model(&MemoryItem{}).Where("scope = ? AND scope_id = ? AND "+quoteCol(tx, "key")+" = ? AND id <> ?", out.Scope, out.ScopeID, out.Key, id).
				Count(&clash).Error; err != nil {
				return err
			}
			if clash > 0 {
				return invalidMemory("同一范围内已存在相同 key 的记忆")
			}
			out.UpdatedAt = time.Now()
			return tx.Model(&MemoryItem{}).Where("id = ?", id).Updates(map[string]any{
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
	res := r.db.Delete(&MemoryItem{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrMemoryNotFound
	}
	return nil
}

// MemoryScopeRef names one (scope, scope_id) pair.
type MemoryScopeRef struct {
	Scope   string
	ScopeID uint
}

// MemorySearch is a recall query: every whitespace-separated term of Query
// must appear (case-insensitively) in key or content. An empty Query matches
// everything in Scopes.
type MemorySearch struct {
	Query  string
	Scopes []MemoryScopeRef
	Limit  int
}

// SearchMemory runs a recall query, ranked like ListMemory.
func (r *Repository) SearchMemory(q MemorySearch) ([]MemoryItem, error) {
	if len(q.Scopes) == 0 {
		return nil, nil
	}
	tx := r.db.Model(&MemoryItem{})
	scopeCond := r.db.Where("1 = 0")
	for _, s := range q.Scopes {
		scopeCond = scopeCond.Or("scope = ? AND scope_id = ?", s.Scope, s.ScopeID)
	}
	tx = tx.Where(scopeCond)
	for _, term := range strings.Fields(q.Query) {
		pat := "%" + likeLiteral(term) + "%"
		tx = tx.Where("("+ciLikeCondition(tx, quoteCol(tx, "key"))+" OR "+ciLikeCondition(tx, "content")+")", pat, pat)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	var items []MemoryItem
	err := tx.Order(memoryOrder).Limit(limit).Find(&items).Error
	return items, err
}

// BumpMemoryHits records that the items were used (recall hit / injected).
func (r *Repository) BumpMemoryHits(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.Model(&MemoryItem{}).Where("id IN ?", ids).
		Updates(map[string]any{"hits": gorm.Expr("hits + 1"), "last_used_at": time.Now()}).Error
}

// NotesSince returns the session's shared notes with id > afterID, oldest
// first (limit <= 0 means 50). Workers keep afterID as their last-seen cursor.
func (r *Repository) NotesSince(sessionID, afterID uint, limit int) ([]MemoryItem, error) {
	if limit <= 0 {
		limit = 50
	}
	var items []MemoryItem
	err := r.db.Where("scope = ? AND scope_id = ? AND id > ?", MemoryScopeSession, sessionID, afterID).
		Order("id ASC").Limit(limit).Find(&items).Error
	return items, err
}

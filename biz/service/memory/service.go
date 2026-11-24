// Package memory manages the notes the agent reads: working notes of a
// session, and conventions that persist per storage or globally.
package memory

import (
	"context"
	"errors"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/errs"
	"FileEngine/internal/db"
)

var errNoMemory = errs.NotFound("记忆不存在")

type Service struct{ repo *db.Repository }

func NewService(repo *db.Repository) *Service { return &Service{repo: repo} }

// List filters by scope ("" = all) and, when given, scope id.
func (s *Service) List(ctx context.Context, scope string, scopeID *uint) ([]model.MemoryItem, error) {
	if scope != "" && !db.ValidMemoryScope(scope) {
		return nil, errs.Invalid("记忆范围无效")
	}
	return s.repo.ListMemory(scope, scopeID)
}

type CreateInput struct {
	Scope   string
	ScopeID uint
	Kind    string
	// empty = derived by db.UserMemoryKey
	Key     string
	Content string
	Path    string
	Pinned  bool
}

// Create adds a note, or updates the live item with the same key; created
// tells which.
func (s *Service) Create(ctx context.Context, in CreateInput) (*model.MemoryItem, bool, error) {
	if in.Key == "" {
		in.Key = db.UserMemoryKey(in.Content, in.Path)
	}
	m := &model.MemoryItem{Scope: in.Scope, ScopeID: in.ScopeID, Kind: in.Kind, Key: in.Key,
		Content: in.Content, Path: in.Path, Pinned: in.Pinned, Source: db.MemorySourceUser}
	created, err := s.repo.UpsertMemory(m)
	if err != nil {
		return nil, false, translate(err)
	}
	emit(m)
	return m, created, nil
}

type Patch struct {
	Kind, Key, Content, Path *string
	Pinned                   *bool
}

// Update edits an item; the user becomes its source, which protects it from
// reflection. A user note whose text or folder changed gets its key re-derived
// unless the caller names one.
func (s *Service) Update(ctx context.Context, id uint, p Patch) (*model.MemoryItem, error) {
	m, err := s.repo.UpdateMemory(id, func(m *model.MemoryItem) {
		wasUser := m.Source == db.MemorySourceUser
		if p.Kind != nil {
			m.Kind = *p.Kind
		}
		if p.Content != nil {
			m.Content = *p.Content
		}
		if p.Path != nil {
			m.Path = *p.Path
		}
		if p.Pinned != nil {
			m.Pinned = *p.Pinned
		}
		switch {
		case p.Key != nil:
			m.Key = *p.Key
		case wasUser && (p.Content != nil || p.Path != nil):
			m.Key = db.UserMemoryKey(m.Content, m.Path)
		}
		m.Source = db.MemorySourceUser
	})
	if err != nil {
		return nil, translate(err)
	}
	emit(m)
	return m, nil
}

// Delete removes an item; one that is already gone is a success.
func (s *Service) Delete(ctx context.Context, id uint) error {
	m, err := s.repo.GetMemory(id)
	if errors.Is(err, db.ErrMemoryNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.repo.DeleteMemory(m.ID); err != nil && !errors.Is(err, db.ErrMemoryNotFound) {
		return err
	}
	emit(m)
	return nil
}

func translate(err error) error {
	var invalid *db.MemoryValidationError
	switch {
	case errors.Is(err, db.ErrMemoryNotFound):
		return errNoMemory
	case errors.As(err, &invalid):
		return errs.Invalid(invalid.Msg)
	}
	return err
}

func emit(m *model.MemoryItem) {
	if m.Scope == db.MemoryScopeSession {
		db.Emit(m.ScopeID, 0, db.EvMemoryUpdated, db.MemoryUpdatedData{Scope: m.Scope, ID: m.ID})
	}
}

package api

import (
	"errors"
	"net/http"
	"strconv"

	"FileEngine/internal/db"

	"github.com/gin-gonic/gin"
)

// listMemory lists memory items: GET /memory?scope=&scope_id= → {items}.
func (s *Server) listMemory(c *gin.Context) {
	scope := c.Query("scope")
	if scope != "" && !db.ValidMemoryScope(scope) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid scope"})
		return
	}
	var scopeID *uint
	if v := c.Query("scope_id"); v != "" {
		id, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid scope_id"})
			return
		}
		u := uint(id)
		scopeID = &u
	}
	items, err := s.repo.ListMemory(scope, scopeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []db.MemoryItem{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type CreateMemoryRequest struct {
	Scope   string `json:"scope"`
	ScopeID uint   `json:"scope_id"`
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Content string `json:"content"`
	Path    string `json:"path"`
	Pinned  bool   `json:"pinned"`
}

// createMemory writes a user memory; an existing (scope, scope_id, key) is
// updated in place (200), a new one is created (201).
func (s *Server) createMemory(c *gin.Context) {
	var req CreateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m := &db.MemoryItem{Scope: req.Scope, ScopeID: req.ScopeID, Kind: req.Kind, Key: req.Key,
		Content: req.Content, Path: req.Path, Pinned: req.Pinned, Source: db.MemorySourceUser}
	created, err := s.repo.UpsertMemory(m)
	if err != nil {
		writeMemoryError(c, err)
		return
	}
	emitMemoryChange(m)
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	c.JSON(code, m)
}

type UpdateMemoryRequest struct {
	Kind    *string `json:"kind"`
	Key     *string `json:"key"`
	Content *string `json:"content"`
	Path    *string `json:"path"`
	Pinned  *bool   `json:"pinned"`
}

// updateMemory edits one item; the user becomes its source (user-curated
// items are protected from reflection).
func (s *Server) updateMemory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req UpdateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m, err := s.repo.UpdateMemory(uint(id), func(m *db.MemoryItem) {
		if req.Kind != nil {
			m.Kind = *req.Kind
		}
		if req.Key != nil {
			m.Key = *req.Key
		}
		if req.Content != nil {
			m.Content = *req.Content
		}
		if req.Path != nil {
			m.Path = *req.Path
		}
		if req.Pinned != nil {
			m.Pinned = *req.Pinned
		}
		m.Source = db.MemorySourceUser
	})
	if err != nil {
		writeMemoryError(c, err)
		return
	}
	emitMemoryChange(m)
	c.JSON(http.StatusOK, m)
}

func (s *Server) deleteMemory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	m, err := s.repo.GetMemory(uint(id))
	if err != nil {
		writeMemoryError(c, err)
		return
	}
	if err := s.repo.DeleteMemory(m.ID); err != nil {
		writeMemoryError(c, err)
		return
	}
	emitMemoryChange(m)
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func writeMemoryError(c *gin.Context, err error) {
	var invalid *db.MemoryValidationError
	switch {
	case errors.Is(err, db.ErrMemoryNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.As(err, &invalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// emitMemoryChange publishes memory_updated for session-scoped items (the
// only ones tied to a session stream).
func emitMemoryChange(m *db.MemoryItem) {
	if m.Scope == db.MemoryScopeSession {
		db.Emit(m.ScopeID, 0, db.EvMemoryUpdated, db.MemoryUpdatedData{Scope: m.Scope, ID: m.ID})
	}
}

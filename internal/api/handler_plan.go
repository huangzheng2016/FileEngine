package api

import (
	"errors"
	"net/http"
	"strconv"

	"FileEngine/internal/db"

	"github.com/gin-gonic/gin"
)

// loadSession resolves the :id session param, writing the error response
// itself when it fails.
func (s *Server) loadSession(c *gin.Context) (*db.ScanSession, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return nil, false
	}
	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return nil, false
	}
	return session, true
}

// getPlan returns the session plan; a session without one gets {version: 0}.
func (s *Server) getPlan(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	plan, err := s.repo.GetPlan(session.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, plan)
}

type UpdatePlanRequest struct {
	Content string `json:"content"`
	// Version is the plan version the edit is based on (0 when creating).
	Version int `json:"version"`
}

// updatePlan saves the user's edit of the plan; a stale version is a 409
// carrying the current plan.
func (s *Server) updatePlan(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	var req UpdatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Version < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "version must be >= 0"})
		return
	}
	plan, err := s.repo.SavePlan(session.ID, req.Content, req.Version, "")
	if errors.Is(err, db.ErrPlanVersionConflict) {
		cur, _ := s.repo.GetPlan(session.ID)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "plan": cur})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	db.Emit(session.ID, 0, db.EvPlanUpdated, db.PlanUpdatedData{Version: plan.Version, Status: plan.Status})
	c.JSON(http.StatusOK, plan)
}

// approvePlan marks the plan approved.
func (s *Server) approvePlan(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	plan, err := s.repo.ApprovePlan(session.ID)
	if errors.Is(err, db.ErrPlanNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	db.Emit(session.ID, 0, db.EvPlanUpdated, db.PlanUpdatedData{Version: plan.Version, Status: plan.Status})
	c.JSON(http.StatusOK, plan)
}

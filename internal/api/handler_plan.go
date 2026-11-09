package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"FileEngine/internal/agent"
	"FileEngine/internal/db"

	"github.com/gin-gonic/gin"
)

func (s *Server) loadSession(c *gin.Context) (*db.ScanSession, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return nil, false
	}
	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		lookupFailed(c, err, msgNoSession)
		return nil, false
	}
	return session, true
}

func (s *Server) getPlan(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	plan, err := s.repo.GetPlan(session.ID)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, plan)
}

type UpdatePlanRequest struct {
	Content string `json:"content"`
	Version int    `json:"version"`
}

func (s *Server) updatePlan(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	var req UpdatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badBody(c, err)
		return
	}
	if req.Version < 0 {
		badRequest(c, "version 不能为负数")
		return
	}
	plan, err := s.repo.SavePlan(session.ID, req.Content, req.Version, "")
	if errors.Is(err, db.ErrPlanVersionConflict) {
		cur, _ := s.repo.GetPlan(session.ID)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "plan": cur})
		return
	}
	if err != nil {
		internalError(c, err)
		return
	}
	db.Emit(session.ID, 0, db.EvPlanUpdated, db.PlanUpdatedData{Version: plan.Version, Status: plan.Status})
	c.JSON(http.StatusOK, plan)
}

func (s *Server) approvePlan(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	plan, err := s.repo.ApprovePlan(session.ID)
	if errors.Is(err, db.ErrPlanNotFound) {
		notFound(c, err.Error())
		return
	}
	if err != nil {
		internalError(c, err)
		return
	}
	db.Emit(session.ID, 0, db.EvPlanUpdated, db.PlanUpdatedData{Version: plan.Version, Status: plan.Status})
	c.JSON(http.StatusOK, plan)
}

type DraftPlanRequest struct {
	Instructions string `json:"instructions"`
}

func (s *Server) draftPlan(c *gin.Context) {
	var req DraftPlanRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			badBody(c, err)
			return
		}
	}
	session, a, ok := s.agentForSession(c)
	if !ok {
		return
	}
	if session.Status == "scanning" {
		conflict(c, "整理任务还在扫描，扫描完成后再生成方案")
		return
	}
	run, err := s.launchRun(session.ID, db.RunPhaseSurvey, "生成方案", func(ctx context.Context, run *db.AgentRun) runOutcome {
		logger := agent.NewLogger(session.ID, run.ID, 0)
		summary, err := a.RunSurvey(ctx, logger, req.Instructions)
		return runOutcome{Summary: summary, Err: err, Tokens: logger.Tokens()}
	})
	respondRunStart(c, run, err)
}

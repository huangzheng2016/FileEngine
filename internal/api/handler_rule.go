package api

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"FileEngine/internal/db"
	"FileEngine/internal/scanner"

	"github.com/gin-gonic/gin"
)

func installRuleHook(repo *db.Repository) {
	scanner.ApplyRulesHook = func(sessionID uint) {
		if err := repo.PrepareSession(sessionID); err != nil {
			log.Printf("session %d: apply rules after scan: %v", sessionID, err)
		}
	}
}

type RuleRequest struct {
	Name       *string `json:"name"`
	Exts       *string `json:"exts"`
	NameRegex  *string `json:"name_regex"`
	PathPrefix *string `json:"path_prefix"`
	FileType   *string `json:"file_type"`
	MinSize    *int64  `json:"min_size"`
	MaxSize    *int64  `json:"max_size"`
	Target     *string `json:"target"`
	Role       *string `json:"role"`
	Position   *int    `json:"position"`
	Source     *string `json:"source"`
}

func (req *RuleRequest) apply(r *db.PlanRule) {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&r.Name, req.Name)
	set(&r.Exts, req.Exts)
	set(&r.NameRegex, req.NameRegex)
	set(&r.PathPrefix, req.PathPrefix)
	set(&r.FileType, req.FileType)
	set(&r.Target, req.Target)
	set(&r.Role, req.Role)
	set(&r.Source, req.Source)
	if req.MinSize != nil {
		r.MinSize = *req.MinSize
	}
	if req.MaxSize != nil {
		r.MaxSize = *req.MaxSize
	}
	if req.Position != nil {
		r.Position = *req.Position
	}
}

func writeRuleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, db.ErrRuleNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case db.IsRejection(err):
		badRequest(c, err.Error())
	default:
		internalError(c, err)
	}
}

func (s *Server) listRules(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	rules, err := s.repo.ListRules(session.ID)
	if err != nil {
		internalError(c, err)
		return
	}
	if rules == nil {
		rules = []db.PlanRule{}
	}
	c.JSON(http.StatusOK, gin.H{"items": rules})
}

func (s *Server) createRule(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	var req RuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err.Error())
		return
	}
	rule := &db.PlanRule{SessionID: session.ID, Source: db.MemorySourceUser}
	req.apply(rule)
	if err := s.repo.CreateRule(rule); err != nil {
		writeRuleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, rule)
}

func (s *Server) updateRule(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, "invalid id")
		return
	}
	var req RuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err.Error())
		return
	}
	rule, err := s.repo.UpdateRule(uint(id), req.apply)
	if err != nil {
		writeRuleError(c, err)
		return
	}
	c.JSON(http.StatusOK, rule)
}

func (s *Server) deleteRule(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, "invalid id")
		return
	}
	if err := s.repo.DeleteRule(uint(id)); err != nil {
		writeRuleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (s *Server) applyRules(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	preview := c.Query("preview") == "1" || c.Query("preview") == "true"
	stats, err := s.repo.ApplyRules(session.ID, 0, preview)
	if err != nil {
		writeRuleError(c, err)
		return
	}
	applied, skipped := 0, 0
	for _, st := range stats {
		applied += st.Matched
		skipped += st.Skipped
	}
	c.JSON(http.StatusOK, gin.H{"items": stats, "applied": applied, "skipped": skipped})
}

type FileIDsRequest struct {
	FileIDs []uint `json:"file_ids"`
}

func (s *Server) confirmReview(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	var req FileIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.FileIDs) == 0 {
		badRequest(c, "file_ids required")
		return
	}
	n, err := s.repo.ConfirmReview(session.ID, req.FileIDs)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"confirmed": n})
}

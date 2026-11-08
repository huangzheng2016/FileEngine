package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// previewTree lists one directory of the before/after preview:
// GET /sessions/:id/preview/tree?side=after|before&path=&offset=&limit=
// → {items, total}. side=after browses the virtual target tree ("/"-rooted
// paths); side=before browses the scanned tree with a status per item.
func (s *Server) previewTree(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	dir := c.Query("path")
	switch side := c.DefaultQuery("side", "after"); side {
	case "after":
		page, err := s.repo.PreviewAfter(session.ID, dir, offset, limit)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, page)
	case "before":
		page, err := s.repo.PreviewBefore(session.ID, session.RootPath, dir, offset, limit)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, page)
	default:
		badRequest(c, "side 只能是 after 或 before")
	}
}

type ResetPlansRequest struct {
	FileIDs []uint `json:"file_ids"`
}

// resetPlans clears the targets of the given entries and marks them untagged
// so the next tagging/chat run redoes them: POST /sessions/:id/plans/reset
// {file_ids} → {reset}.
func (s *Server) resetPlans(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	var req ResetPlansRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badBody(c, err)
		return
	}
	if len(req.FileIDs) == 0 {
		badRequest(c, "缺少 file_ids")
		return
	}
	n, err := s.repo.ResetPlans(session.ID, req.FileIDs)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"reset": n})
}

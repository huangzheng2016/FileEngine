package api

import (
	"net/http"
	"strconv"

	"FileEngine/internal/db"

	"github.com/gin-gonic/gin"
)

func (s *Server) listLogs(c *gin.Context) {
	q := db.LogQuery{}

	if sid := c.Query("session_id"); sid != "" {
		if id, err := strconv.ParseUint(sid, 10, 32); err == nil {
			q.SessionID = uint(id)
		}
	}
	if batch := c.Query("batch"); batch != "" {
		if b, err := strconv.Atoi(batch); err == nil {
			q.Batch = &b
		}
	}
	q.Role = c.Query("role")
	q.ToolName = c.Query("tool_name")
	q.Order = c.Query("order") // "asc" or "desc"
	if page := c.Query("page"); page != "" {
		q.Page, _ = strconv.Atoi(page)
	}
	if ps := c.Query("page_size"); ps != "" {
		q.PageSize, _ = strconv.Atoi(ps)
	}

	logs, total, err := s.repo.ListAgentLogs(q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs, "total": total})
}

func (s *Server) listBatches(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Query("session_id"), 10, 32)
	if sessionID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	batches, total, err := s.repo.ListBatches(uint(sessionID), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"batches": batches, "total": total})
}

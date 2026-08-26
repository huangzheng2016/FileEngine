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

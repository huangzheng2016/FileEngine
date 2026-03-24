package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	"FileEngine/internal/db"

	"github.com/gin-gonic/gin"
)

var (
	sseListenersMu sync.RWMutex
	sseListeners   = make(map[uint][]chan *db.AgentLog)
)

func RegisterSSEListener(sessionID uint, ch chan *db.AgentLog) {
	sseListenersMu.Lock()
	defer sseListenersMu.Unlock()
	sseListeners[sessionID] = append(sseListeners[sessionID], ch)
}

func UnregisterSSEListener(sessionID uint, ch chan *db.AgentLog) {
	sseListenersMu.Lock()
	defer sseListenersMu.Unlock()
	listeners := sseListeners[sessionID]
	for i, l := range listeners {
		if l == ch {
			sseListeners[sessionID] = append(listeners[:i], listeners[i+1:]...)
			break
		}
	}
}

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

func (s *Server) streamLogs(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Query("session_id"), 10, 32)
	if sessionID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	// Subscribe to agent logger if running
	agentMu.Lock()
	a, ok := agents[uint(sessionID)]
	agentMu.Unlock()

	var logCh chan *db.AgentLog
	if ok {
		logCh = a.GetLogger().Subscribe()
		defer a.GetLogger().Unsubscribe(logCh)
	} else {
		logCh = make(chan *db.AgentLog, 100)
		RegisterSSEListener(uint(sessionID), logCh)
		defer UnregisterSSEListener(uint(sessionID), logCh)
	}

	ctx := c.Request.Context()
	c.Stream(func(w io.Writer) bool {
		select {
		case <-ctx.Done():
			return false
		case log, ok := <-logCh:
			if !ok {
				return false
			}
			data, _ := json.Marshal(log)
			fmt.Fprintf(w, "data: %s\n\n", data)
			return true
		}
	})
}

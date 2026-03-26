package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"

	"FileEngine/internal/db"

	"github.com/coder/websocket"
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

func (s *Server) streamLogs(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Query("session_id"), 10, 32)
	if sessionID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
		return
	}

	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("websocket accept: %v", err)
		return
	}
	defer conn.CloseNow()

	ctx := conn.CloseRead(c.Request.Context())

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

	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case entry, ok := <-logCh:
			if !ok {
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			data, _ := json.Marshal(entry)
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		}
	}
}

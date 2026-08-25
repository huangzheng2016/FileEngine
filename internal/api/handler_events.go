package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"FileEngine/internal/db"

	"github.com/gin-gonic/gin"
)

const sseHeartbeatInterval = 25 * time.Second

// streamSessionEvents serves the session event stream over SSE
// (GET /api/v1/sessions/:id/events/stream). It is the single live channel for
// agent run events (run_status / batch_start / message / tool_call /
// tool_result / progress / run_done — see internal/db/events.go). History
// stays available via GET /logs; this endpoint only streams new events.
func (s *Server) streamSessionEvents(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	sessionID := uint(id)
	if _, err := s.repo.GetSession(sessionID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	ch := db.SubscribeEvents(sessionID)
	defer db.UnsubscribeEvents(sessionID, ch)

	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()

	// Leading comment so the client's EventSource fires `open` immediately.
	_, _ = fmt.Fprintf(c.Writer, ": session %d event stream\n\n", sessionID)
	flusher.Flush()

	ctx := c.Request.Context()
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			data, err := json.Marshal(ev.Payload)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			// SSE comment heartbeat: keeps intermediaries from idle-closing
			// the connection without producing client-side events.
			if _, err := fmt.Fprint(c.Writer, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// publishRunStatus emits a run_status event mirroring a phase transition.
func publishRunStatus(sessionID uint, phase, status, message string) {
	db.PublishEvent(db.Event{Type: db.EventRunStatus, SessionID: sessionID, Payload: db.RunStatusPayload{
		SessionID: sessionID,
		Phase:     phase,
		Status:    status,
		Message:   message,
	}})
}

// publishRunDone emits the exactly-once terminal run_done event of a run.
func publishRunDone(sessionID uint, phase, status, summary string) {
	db.PublishEvent(db.Event{Type: db.EventRunDone, SessionID: sessionID, Payload: db.RunDonePayload{
		SessionID: sessionID,
		Phase:     phase,
		Status:    status,
		Summary:   summary,
	}})
}

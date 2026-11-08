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

// sseHeartbeatInterval is a var so tests can shrink it.
var sseHeartbeatInterval = 20 * time.Second

const replayPageSize = 500

// eventItem is one persisted event in the history API.
type eventItem struct {
	Seq       uint            `json:"seq"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

func queryUint(c *gin.Context, key string) (uint, error) {
	v := c.Query(key)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("参数 %s 无效", key)
	}
	return uint(n), nil
}

// listSessionEvents serves GET /sessions/:id/events?run_id=&after=&before=&limit=
// → {items (ascending seq), has_more}. With only before set it pages
// backwards (the newest events below before).
func (s *Server) listSessionEvents(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, "无效的会话 ID")
		return
	}
	q := db.EventQuery{SessionID: uint(id)}
	for key, dst := range map[string]*uint{"run_id": &q.RunID, "after": &q.After, "before": &q.Before} {
		if *dst, err = queryUint(c, key); err != nil {
			badRequest(c, err.Error())
			return
		}
	}
	limit, err := queryUint(c, "limit")
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	q.Limit = int(limit)
	rows, more, err := s.repo.ListEvents(q)
	if err != nil {
		internalError(c, err)
		return
	}
	items := make([]eventItem, len(rows))
	for i, ev := range rows {
		items[i] = eventItem{Seq: ev.ID, Type: ev.Type, Data: json.RawMessage(ev.Data), CreatedAt: ev.CreatedAt}
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "has_more": more})
}

// streamSessionEvents serves the session event stream over SSE
// (GET /sessions/:id/events/stream[?run_id=]). Frames carry `id: <seq>`.
// Resume point: the Last-Event-ID header or ?after=<seq>; without either the
// replay starts at the latest run's first event. The handler subscribes
// first, replays persisted events after the resume point, then follows the
// live broadcast, dropping live events it already replayed. A subscriber
// that lagged (dropped events) re-reads the gap from the database.
func (s *Server) streamSessionEvents(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, "无效的会话 ID")
		return
	}
	sessionID := uint(id)
	if _, err := s.repo.GetSession(sessionID); err != nil {
		lookupFailed(c, err, msgNoSession)
		return
	}
	runID, err := queryUint(c, "run_id")
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	var after uint
	resume := c.GetHeader("Last-Event-ID")
	if resume == "" {
		resume = c.Query("after")
	}
	switch {
	case resume != "":
		n, err := strconv.ParseUint(resume, 10, 64)
		if err != nil {
			badRequest(c, "Last-Event-ID 无效")
			return
		}
		after = uint(n)
	case runID == 0:
		if after, err = s.repo.LatestRunStartSeq(sessionID); err != nil {
			internalError(c, err)
			return
		}
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		abortError(c, http.StatusInternalServerError, "不支持流式响应")
		return
	}

	sub := db.Subscribe(sessionID)
	defer db.Unsubscribe(sub)

	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()
	// Leading comment so the client's EventSource fires `open` immediately.
	if _, err := fmt.Fprint(c.Writer, ": stream\n\n"); err != nil {
		return
	}
	flusher.Flush()

	last := after
	write := func(ev db.SessionEvent) bool {
		if ev.ID <= last || (runID > 0 && ev.RunID != runID) {
			return true
		}
		if _, err := fmt.Fprintf(c.Writer, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, ev.Data); err != nil {
			return false
		}
		last = ev.ID
		return true
	}
	replay := func() bool {
		for {
			rows, more, err := s.repo.ListEvents(db.EventQuery{SessionID: sessionID, RunID: runID, After: last, Limit: replayPageSize})
			if err != nil {
				return false
			}
			for _, ev := range rows {
				if !write(ev) {
					return false
				}
			}
			flusher.Flush()
			if !more {
				return true
			}
		}
	}
	if !replay() {
		return
	}

	ctx := c.Request.Context()
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			if sub.TakeLagged() {
				if !replay() {
					return
				}
				continue
			}
			if !write(ev) {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if sub.TakeLagged() && !replay() {
				return
			}
			if _, err := fmt.Fprint(c.Writer, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

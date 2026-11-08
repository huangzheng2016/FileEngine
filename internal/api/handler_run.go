package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"FileEngine/internal/db"

	"github.com/gin-gonic/gin"
)

// runOutcome is what a run's work function reports back to launchRun.
type runOutcome struct {
	Summary string
	Partial bool // tagging finished with failed batches
	Err     error
	Tokens  db.TokenCounts
}

// runStopWait bounds how long POST /runs/:id/stop waits for the run to wind
// down before answering. A var so tests can shrink it.
var runStopWait = 30 * time.Second

var runDone = struct {
	mu sync.Mutex
	m  map[uint]chan struct{}
}{m: make(map[uint]chan struct{})}

// launchRun creates a run of the session (db.ErrSessionBusy when another run
// is running) and executes work in the background on a stoppable context.
// The run ends done / partial / stopped (context canceled) / error according
// to the outcome; FinishRun emits the terminal events.
func (s *Server) launchRun(sessionID uint, phase, title string, work func(ctx context.Context, run *db.AgentRun) runOutcome) (*db.AgentRun, error) {
	run, err := s.repo.CreateRun(sessionID, phase, title)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	db.RegisterRunCancel(run.ID, cancel)
	done := make(chan struct{})
	runDone.mu.Lock()
	runDone.m[run.ID] = done
	runDone.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			runDone.mu.Lock()
			delete(runDone.m, run.ID)
			runDone.mu.Unlock()
			close(done)
		}()
		out := work(ctx, run)
		status, errMsg := db.RunDone, ""
		switch {
		case out.Err != nil && (errors.Is(out.Err, context.Canceled) || ctx.Err() != nil):
			status = db.RunStopped
		case out.Err != nil:
			status, errMsg = db.RunError, out.Err.Error()
			log.Printf("%s run %d of session %d failed: %v", phase, run.ID, sessionID, out.Err)
		case out.Partial:
			status = db.RunPartial
		}
		if err := s.repo.FinishRun(run, status, out.Summary, errMsg, out.Tokens); err != nil {
			log.Printf("finish run %d: %v", run.ID, err)
		}
	}()
	return run, nil
}

// respondRunStart answers a run-starting request: {run_id} on success, 409
// when the session is busy.
func respondRunStart(c *gin.Context, run *db.AgentRun, err error) {
	if errors.Is(err, db.ErrSessionBusy) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_id": run.ID})
}

func (s *Server) listRuns(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的会话 ID"})
		return
	}
	runs, err := s.repo.ListRuns(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if runs == nil {
		runs = []db.AgentRun{}
	}
	c.JSON(http.StatusOK, gin.H{"items": runs})
}

func (s *Server) getRun(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("run_id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的运行 ID"})
		return
	}
	run, err := s.repo.GetRun(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "运行不存在"})
		return
	}
	c.JSON(http.StatusOK, run)
}

// stopRun cancels a running run and waits (bounded) for it to wind down; it
// answers with the run as stored afterwards. A running row without a live
// goroutine is finished as stopped directly.
func (s *Server) stopRun(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("run_id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的运行 ID"})
		return
	}
	runID := uint(id)
	run, err := s.repo.GetRun(runID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "运行不存在"})
		return
	}
	if run.Status == db.RunRunning {
		runDone.mu.Lock()
		done := runDone.m[runID]
		runDone.mu.Unlock()
		if db.CancelRun(runID) && done != nil {
			select {
			case <-done:
			case <-time.After(runStopWait):
				log.Printf("stop run %d: still winding down after %s", runID, runStopWait)
			}
		} else if done == nil {
			_ = s.repo.FinishRun(run, db.RunStopped, "", "", db.TokenCounts{})
		}
		if run, err = s.repo.GetRun(runID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, run)
}

// stopSessionRuns stops every running run of a session (used before the
// session is deleted).
func (s *Server) stopSessionRuns(sessionID uint) {
	runs, err := s.repo.RunningRuns(sessionID)
	if err != nil {
		return
	}
	for _, r := range runs {
		runDone.mu.Lock()
		done := runDone.m[r.ID]
		runDone.mu.Unlock()
		if db.CancelRun(r.ID) && done != nil {
			select {
			case <-done:
			case <-time.After(runStopWait):
			}
		}
	}
}

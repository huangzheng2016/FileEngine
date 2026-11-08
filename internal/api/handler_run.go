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

type runOutcome struct {
	Summary string
	Partial bool
	Err     error
	Tokens  db.TokenCounts
	Then    func(status string)
}

var runStopWait = 30 * time.Second

var runDone = struct {
	mu sync.Mutex
	m  map[uint]chan struct{}
}{m: make(map[uint]chan struct{})}

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
		if out.Then != nil {
			out.Then(status)
		}
	}()
	return run, nil
}

func respondRunStart(c *gin.Context, run *db.AgentRun, err error) {
	if errors.Is(err, db.ErrSessionBusy) {
		conflict(c, err.Error())
		return
	}
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_id": run.ID})
}

func (s *Server) listRuns(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, "无效的会话 ID")
		return
	}
	runs, err := s.repo.ListRuns(uint(id))
	if err != nil {
		internalError(c, err)
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
		badRequest(c, "无效的运行 ID")
		return
	}
	run, err := s.repo.GetRun(uint(id))
	if err != nil {
		lookupFailed(c, err, msgNoRun)
		return
	}
	c.JSON(http.StatusOK, run)
}

// A running row without a live goroutine is finished as stopped directly.
func (s *Server) stopRun(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("run_id"), 10, 32)
	if err != nil {
		badRequest(c, "无效的运行 ID")
		return
	}
	runID := uint(id)
	run, err := s.repo.GetRun(runID)
	if err != nil {
		lookupFailed(c, err, msgNoRun)
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
			internalError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, run)
}

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

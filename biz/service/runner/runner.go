// Package runner owns the process-level lifecycle of agent runs and scans:
// launching them on detached contexts, stopping them, and draining them at
// shutdown. Resource services call Launch / StartScan; the app shuts them down.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/db"
)

// Outcome is what a run's work function reports back.
type Outcome struct {
	Summary string
	Partial bool
	Err     error
	Tokens  db.TokenCounts
	// Then runs after the run is finished, with its final status.
	Then func(status string)
}

type Service struct {
	repo     *db.Repository
	stopWait time.Duration

	mu    sync.Mutex
	done  map[uint]chan struct{}
	scans map[uint]*scanHandle
}

// done closes when the goroutine has fully exited, all DB writes included.
type scanHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func NewService(repo *db.Repository) *Service {
	return &Service{
		repo:     repo,
		stopWait: 30 * time.Second,
		done:     make(map[uint]chan struct{}),
		scans:    make(map[uint]*scanHandle),
	}
}

// Launch records a run and executes work on a detached context. A panic in work
// finishes the run as an error instead of taking the process down.
func (s *Service) Launch(sessionID uint, phase, title string, work func(ctx context.Context, run *model.AgentRun) Outcome) (*model.AgentRun, error) {
	run, err := s.repo.CreateRun(sessionID, phase, title)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	db.RegisterRunCancel(run.ID, cancel)
	done := make(chan struct{})
	s.mu.Lock()
	s.done[run.ID] = done
	s.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.done, run.ID)
			s.mu.Unlock()
			close(done)
		}()
		out := safeWork(ctx, run, work)
		status, errMsg := db.RunDone, ""
		switch {
		case out.Err != nil && (errors.Is(out.Err, context.Canceled) || ctx.Err() != nil):
			status = db.RunStopped
		case out.Err != nil:
			status, errMsg = db.RunError, out.Err.Error()
			slog.Error("run failed", "phase", phase, "run", run.ID, "session", sessionID, "err", out.Err)
		case out.Partial:
			status = db.RunPartial
		}
		if err := s.repo.FinishRun(run, status, out.Summary, errMsg, out.Tokens); err != nil {
			slog.Error("finish run", "run", run.ID, "err", err)
		}
		if out.Then != nil {
			out.Then(status)
		}
	}()
	return run, nil
}

func safeWork(ctx context.Context, run *model.AgentRun, work func(context.Context, *model.AgentRun) Outcome) (out Outcome) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("run panicked", "run", run.ID, "panic", p, "stack", string(debug.Stack()))
			out = Outcome{Err: fmt.Errorf("内部错误")}
		}
	}()
	return work(ctx, run)
}

// Stop cancels a running run, waits for it to wind down and returns its final row.
func (s *Service) Stop(runID uint) (*model.AgentRun, error) {
	run, err := s.repo.GetRun(runID)
	if err != nil {
		return nil, err
	}
	if run.Status != db.RunRunning {
		return run, nil
	}
	if db.CancelRun(runID) {
		if !s.awaitEnd(runID) {
			slog.Warn("stop run: still winding down", "run", runID, "after", s.stopWait)
		}
	} else {
		// A running row nobody can cancel belongs to no live goroutine.
		_ = s.repo.FinishRun(run, db.RunStopped, "", "", db.TokenCounts{})
	}
	return s.repo.GetRun(runID)
}

// awaitEnd waits until the run's goroutine has exited (runs the runner
// launched) or its row left running (the executor's runs, which finish their
// own row), for at most the stop wait.
func (s *Service) awaitEnd(runID uint) bool {
	s.mu.Lock()
	done := s.done[runID]
	s.mu.Unlock()
	deadline := time.After(s.stopWait)
	if done != nil {
		select {
		case <-done:
			return true
		case <-deadline:
			return false
		}
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if r, err := s.repo.GetRun(runID); err != nil || r.Status != db.RunRunning {
			return true
		}
		select {
		case <-tick.C:
		case <-deadline:
			return false
		}
	}
}

// StopSession stops every running run of the session and waits for them.
func (s *Service) StopSession(sessionID uint) {
	runs, err := s.repo.RunningRuns(sessionID)
	if err != nil {
		return
	}
	for _, r := range runs {
		if db.CancelRun(r.ID) {
			s.awaitEnd(r.ID)
		}
	}
}

// StartScan runs scan on a detached context and registers it under the session.
// scan must return promptly once its context is cancelled.
func (s *Service) StartScan(sessionID uint, scan func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &scanHandle{cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.scans[sessionID] = h
	s.mu.Unlock()
	go func() {
		// Registered first, runs last: waiters on done wake only after every
		// write and the map detach of this goroutine are complete.
		defer close(h.done)
		defer func() {
			s.mu.Lock()
			// Detach only if still the registered scan: a rescan that raced past
			// the stop wait may have replaced this one.
			if s.scans[sessionID] == h {
				delete(s.scans, sessionID)
			}
			s.mu.Unlock()
		}()
		defer func() {
			if p := recover(); p != nil {
				slog.Error("scan panicked", "session", sessionID, "panic", p, "stack", string(debug.Stack()))
				if err := s.repo.FailScan(sessionID, "内部错误"); err != nil {
					slog.Error("fail scan after panic", "session", sessionID, "err", err)
				}
			}
		}()
		scan(ctx)
	}()
}

// CancelScan cancels the session's scan and waits for its last writes. It
// reports false when the scan did not stop within the stop wait.
func (s *Service) CancelScan(sessionID uint) bool {
	s.mu.Lock()
	h := s.scans[sessionID]
	s.mu.Unlock()
	if h == nil {
		return true
	}
	h.cancel()
	select {
	case <-h.done:
		return true
	case <-time.After(s.stopWait):
		return false
	}
}

// Shutdown cancels every run and scan and waits for them until ctx ends. The
// executor stops before its next step, never mid-delete.
func (s *Service) Shutdown(ctx context.Context) error {
	if n := db.CancelAllRuns(); n > 0 {
		slog.Info("shutdown: cancelled runs", "count", n)
	}
	s.mu.Lock()
	scans := make([]*scanHandle, 0, len(s.scans))
	for _, h := range s.scans {
		scans = append(scans, h)
	}
	s.mu.Unlock()
	for _, h := range scans {
		h.cancel()
	}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		s.mu.Lock()
		scanning := len(s.scans)
		s.mu.Unlock()
		if live := db.LiveRunCount(); live == 0 && scanning == 0 {
			// Last: runs and scans schedule counter refreshes, whose timers must
			// not fire after the database closes.
			db.FlushCounts()
			return nil
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return fmt.Errorf("%d run(s) and %d scan(s) did not finish: %w", db.LiveRunCount(), scanning, ctx.Err())
		}
	}
}

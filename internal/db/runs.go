package db

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"sync"
	"time"
)

// ErrSessionBusy is returned when a session already has a running run.
var ErrSessionBusy = errors.New("会话正在运行中")

var runCancels = struct {
	mu sync.Mutex
	m  map[uint]context.CancelFunc
}{m: make(map[uint]context.CancelFunc)}

// CreateRun inserts a running run for the session, refusing when another run
// of the session is still running.
func (r *Repository) CreateRun(sessionID uint, phase, title string) (*AgentRun, error) {
	run := &AgentRun{SessionID: sessionID, Phase: phase, Status: RunRunning, Title: title, StartedAt: time.Now()}
	err := WithRetry(func() error {
		return r.DB().Transaction(func(tx *gorm.DB) error {
			var n int64
			if err := tx.Model(&AgentRun{}).Where("session_id = ? AND status = ?", sessionID, RunRunning).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return ErrSessionBusy
			}
			return tx.Create(run).Error
		})
	})
	if err != nil {
		return nil, err
	}
	Emit(sessionID, run.ID, EvRunStatus, RunStatusData{RunID: run.ID, Phase: phase, Status: RunRunning, Title: title})
	return run, nil
}

// FinishRun records the terminal state of a run and emits run_done exactly once.
func (r *Repository) FinishRun(run *AgentRun, status, summary, errMsg string, tokens TokenCounts) error {
	now := time.Now()
	run.Status, run.Summary, run.Error, run.EndedAt = status, summary, errMsg, &now
	run.PromptTokens, run.CachedTokens, run.CompletionTokens, run.TotalTokens = tokens.Prompt, tokens.Cached, tokens.Completion, tokens.Total
	err := WithRetry(func() error {
		return r.DB().Model(&AgentRun{}).Where("id = ? AND status = ?", run.ID, RunRunning).Updates(map[string]any{
			"status": status, "summary": summary, "error": errMsg, "ended_at": now,
			"prompt_tokens": tokens.Prompt, "cached_tokens": tokens.Cached,
			"completion_tokens": tokens.Completion, "total_tokens": tokens.Total,
		}).Error
	})
	UnregisterRunCancel(run.ID)
	Emit(run.SessionID, run.ID, EvRunDone, RunDoneData{RunID: run.ID, Phase: run.Phase, Status: status, Summary: summary, Error: errMsg, Tokens: tokens})
	return err
}

// RegisterRunCancel makes a run stoppable through CancelRun.
func RegisterRunCancel(runID uint, cancel context.CancelFunc) {
	runCancels.mu.Lock()
	runCancels.m[runID] = cancel
	runCancels.mu.Unlock()
}

func UnregisterRunCancel(runID uint) {
	runCancels.mu.Lock()
	delete(runCancels.m, runID)
	runCancels.mu.Unlock()
}

// CancelRun cancels a running run's context; it reports whether the run was live.
func CancelRun(runID uint) bool {
	runCancels.mu.Lock()
	cancel, ok := runCancels.m[runID]
	runCancels.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Emit publishes one stream event of a session run. data is one of the *Data
// payload types in contract.go.
func Emit(sessionID, runID uint, typ string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	PublishEvent(Event{Type: EventType(typ), SessionID: sessionID, Payload: json.RawMessage(raw)})
}

package db

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
)

var ErrSessionBusy = errors.New("会话正在运行中")

const RunInterruptedError = "服务重启，运行被中断"

var runCancels = struct {
	mu sync.Mutex
	m  map[uint]context.CancelFunc
}{m: make(map[uint]context.CancelFunc)}

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

// Emits the terminal run_status plus run_done exactly once.
func (r *Repository) FinishRun(run *AgentRun, status, summary, errMsg string, tokens TokenCounts) error {
	now := time.Now()
	run.Status, run.Summary, run.Error, run.EndedAt = status, summary, errMsg, &now
	run.PromptTokens, run.CachedTokens, run.CacheWriteTokens = tokens.Prompt, tokens.Cached, tokens.CacheWrite
	run.CompletionTokens, run.TotalTokens = tokens.Completion, tokens.Total
	run.fillCacheHitRate()
	err := WithRetry(func() error {
		return r.DB().Model(&AgentRun{}).Where("id = ? AND status = ?", run.ID, RunRunning).Updates(map[string]any{
			"status": status, "summary": summary, "error": errMsg, "ended_at": now,
			"prompt_tokens": tokens.Prompt, "cached_tokens": tokens.Cached, "cache_write_tokens": tokens.CacheWrite,
			"completion_tokens": tokens.Completion, "total_tokens": tokens.Total,
		}).Error
	})
	UnregisterRunCancel(run.ID)
	Emit(run.SessionID, run.ID, EvRunStatus, RunStatusData{RunID: run.ID, Phase: run.Phase, Status: status, Title: run.Title, Error: errMsg})
	Emit(run.SessionID, run.ID, EvRunDone, RunDoneData{RunID: run.ID, Phase: run.Phase, Status: status, Summary: summary, Error: errMsg, Tokens: tokens})
	return err
}

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

func CancelRun(runID uint) bool {
	runCancels.mu.Lock()
	cancel, ok := runCancels.m[runID]
	runCancels.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// The event is persisted (its row id is the stream seq) and then broadcast;
// text/reasoning deltas are coalesced first.
func Emit(sessionID, runID uint, typ string, data any) {
	emitEvent(sessionID, runID, typ, data)
}

func (r *AgentRun) AfterFind(*gorm.DB) error {
	r.fillCacheHitRate()
	return nil
}

func (r *AgentRun) fillCacheHitRate() {
	r.CacheHitRate = 0
	if r.PromptTokens > 0 {
		r.CacheHitRate = float64(r.CachedTokens) / float64(r.PromptTokens)
	}
}

// Called once on startup, when no run of this process can be live.
func (r *Repository) SweepStaleRuns() (int64, error) {
	res := r.db.Model(&AgentRun{}).Where("status = ?", RunRunning).Updates(map[string]any{
		"status": RunStopped, "error": RunInterruptedError, "ended_at": time.Now(),
	})
	return res.RowsAffected, res.Error
}

func (r *Repository) GetRun(id uint) (*AgentRun, error) {
	var run AgentRun
	err := r.db.First(&run, id).Error
	return &run, err
}

func (r *Repository) ListRuns(sessionID uint) ([]AgentRun, error) {
	var runs []AgentRun
	err := r.db.Where("session_id = ?", sessionID).Order("id DESC").Find(&runs).Error
	return runs, err
}

func (r *Repository) RunningRuns(sessionID uint) ([]AgentRun, error) {
	var runs []AgentRun
	err := r.db.Where("session_id = ? AND status = ?", sessionID, RunRunning).Find(&runs).Error
	return runs, err
}

func (r *Repository) SessionTokens(sessionID uint) (TokenCounts, error) {
	var t TokenCounts
	err := r.db.Model(&AgentRun{}).Where("session_id = ?", sessionID).
		Select(`COALESCE(SUM(prompt_tokens), 0) AS prompt,
			COALESCE(SUM(cached_tokens), 0) AS cached,
			COALESCE(SUM(cache_write_tokens), 0) AS cache_write,
			COALESCE(SUM(completion_tokens), 0) AS completion,
			COALESCE(SUM(total_tokens), 0) AS total`).
		Scan(&t).Error
	return t, err
}

package db

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
)

var ErrSessionBusy = errors.New("整理任务正在运行中")

var (
	ErrRevertOfRevert  = errors.New("撤销不能再撤销")
	ErrAlreadyReverted = errors.New("这次执行已撤销")
)

// A revert that was stopped or failed does not count, so the user can retry.
var effectiveRevertStatuses = []string{RunRunning, RunDone, RunPartial}

const RunInterruptedError = "服务重启，运行被中断"

var runCancels = struct {
	mu sync.Mutex
	m  map[uint]context.CancelFunc
}{m: make(map[uint]context.CancelFunc)}

func (r *Repository) CreateRun(sessionID uint, phase, title string) (*AgentRun, error) {
	run := &AgentRun{SessionID: sessionID, Phase: phase, Title: title}
	return run, r.InsertRun(run)
}

// InsertRun starts run (status running, started now) unless the session
// already has a running run.
func (r *Repository) InsertRun(run *AgentRun) error {
	run.Status, run.StartedAt = RunRunning, time.Now()
	err := WithRetry(func() error {
		return r.DB().Transaction(func(tx *gorm.DB) error {
			var n int64
			if err := tx.Model(&AgentRun{}).Where("session_id = ? AND status = ?", run.SessionID, RunRunning).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return ErrSessionBusy
			}
			if run.RevertsRunID != nil {
				if err := tx.Model(&AgentRun{}).Where("reverts_run_id = ? AND status IN ?", *run.RevertsRunID, effectiveRevertStatuses).Count(&n).Error; err != nil {
					return err
				}
				if n > 0 {
					return ErrAlreadyReverted
				}
			}
			run.ID = 0
			return tx.Create(run).Error
		})
	})
	if err != nil {
		return err
	}
	Emit(run.SessionID, run.ID, EvRunStatus, RunStatusData{RunID: run.ID, Phase: run.Phase, Status: RunRunning, Title: run.Title})
	return nil
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
	if err := r.db.First(&run, id).Error; err != nil {
		return &run, err
	}
	runs := []AgentRun{run}
	err := r.fillRevertedBy(runs)
	return &runs[0], err
}

func (r *Repository) ListRuns(sessionID uint) ([]AgentRun, error) {
	var runs []AgentRun
	if err := r.db.Where("session_id = ?", sessionID).Order("id DESC").Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, r.fillRevertedBy(runs)
}

func (r *Repository) fillRevertedBy(runs []AgentRun) error {
	var ids []uint
	for _, run := range runs {
		if run.Phase == RunPhaseExecuting {
			ids = append(ids, run.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var reverts []AgentRun
	if err := r.db.Select("id", "reverts_run_id").
		Where("reverts_run_id IN ? AND status IN ?", ids, effectiveRevertStatuses).
		Order("id ASC").Find(&reverts).Error; err != nil {
		return err
	}
	by := make(map[uint]uint, len(reverts))
	for _, rv := range reverts {
		by[*rv.RevertsRunID] = rv.ID
	}
	for i := range runs {
		if id, ok := by[runs[i].ID]; ok {
			runs[i].RevertedByRunID = &id
		}
	}
	return nil
}

type RunRef struct {
	ID     uint   `json:"id"`
	Phase  string `json:"phase"`
	Status string `json:"status"`
}

// Latest run per session; nil sessionIDs means every session.
func (r *Repository) LatestRuns(sessionIDs []uint) (map[uint]RunRef, error) {
	latest := r.db.Model(&AgentRun{}).Select("MAX(id)").Group("session_id")
	if sessionIDs != nil {
		if len(sessionIDs) == 0 {
			return map[uint]RunRef{}, nil
		}
		latest = latest.Where("session_id IN ?", sessionIDs)
	}
	var rows []AgentRun
	if err := r.db.Select("id", "session_id", "phase", "status").Where("id IN (?)", latest).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint]RunRef, len(rows))
	for _, run := range rows {
		out[run.SessionID] = RunRef{ID: run.ID, Phase: run.Phase, Status: run.Status}
	}
	return out, nil
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

package db

import (
	"context"
	"errors"
	"sync"
	"time"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrSessionBusy = errors.New("整理任务正在运行中")

var (
	ErrRevertOfRevert  = errors.New("撤销不能再撤销")
	ErrAlreadyReverted = errors.New("这次执行已撤销")
)

// A revert that was stopped or failed does not count, so the user can retry.
var EffectiveRevertStatuses = []string{RunRunning, RunDone, RunPartial}

const RunInterruptedError = "服务重启，运行被中断"

var runCancels = struct {
	mu sync.Mutex
	m  map[uint]context.CancelFunc
}{m: make(map[uint]context.CancelFunc)}

func (r *Repository) CreateRun(sessionID uint, phase, title string) (*model.AgentRun, error) {
	run := &model.AgentRun{SessionID: sessionID, Phase: phase, Title: title}
	return run, r.InsertRun(run)
}

// InsertRun starts run (status running, started now) unless the session
// already has a running run.
func (r *Repository) InsertRun(run *model.AgentRun) error {
	run.Status, run.StartedAt = RunRunning, time.Now()
	err := dal.WithRetry(func() error {
		return r.DB().Transaction(func(tx *gorm.DB) error {
			// Serializes concurrent starts of one session on MySQL/PostgreSQL; the
			// count below would otherwise be a read-then-write race. SQLite has a
			// single writer, so its dialect drops the clause. A missing session
			// row locks nothing and is not an error.
			var locked []model.ScanSession
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", run.SessionID).Find(&locked).Error; err != nil {
				return err
			}
			var n int64
			if err := tx.Model(&model.AgentRun{}).Where("session_id = ? AND status = ?", run.SessionID, RunRunning).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return ErrSessionBusy
			}
			if run.RevertsRunID != nil {
				if err := tx.Model(&model.AgentRun{}).Where("reverts_run_id = ? AND status IN ?", *run.RevertsRunID, EffectiveRevertStatuses).Count(&n).Error; err != nil {
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

// Emits the terminal run_status plus run_done exactly once: only the call whose
// compare-and-set matched the still-running row finishes the run.
func (r *Repository) FinishRun(run *model.AgentRun, status, summary, errMsg string, tokens TokenCounts) error {
	now := time.Now()
	var matched int64
	err := dal.WithRetry(func() error {
		res := r.DB().Model(&model.AgentRun{}).Where("id = ? AND status = ?", run.ID, RunRunning).Updates(map[string]any{
			"status": status, "summary": summary, "error": errMsg, "ended_at": now,
			"prompt_tokens": tokens.Prompt, "cached_tokens": tokens.Cached, "cache_write_tokens": tokens.CacheWrite,
			"completion_tokens": tokens.Completion, "total_tokens": tokens.Total,
		})
		matched = res.RowsAffected
		return res.Error
	})
	UnregisterRunCancel(run.ID)
	if err != nil || matched == 0 {
		return err
	}
	run.Status, run.Summary, run.Error, run.EndedAt = status, summary, errMsg, &now
	run.PromptTokens, run.CachedTokens, run.CacheWriteTokens = tokens.Prompt, tokens.Cached, tokens.CacheWrite
	run.CompletionTokens, run.TotalTokens = tokens.Completion, tokens.Total
	run.FillCacheHitRate()
	Emit(run.SessionID, run.ID, EvRunStatus, RunStatusData{RunID: run.ID, Phase: run.Phase, Status: status, Title: run.Title, Error: errMsg})
	Emit(run.SessionID, run.ID, EvRunDone, RunDoneData{RunID: run.ID, Phase: run.Phase, Status: status, Summary: summary, Error: errMsg, Tokens: tokens})
	return nil
}

// SetRunCounters stores an executing run's final counters. Call it before
// FinishRun so a client that sees run_done also sees them.
func (r *Repository) SetRunCounters(runID uint, total, success, failed, skipped int) error {
	return dal.WithRetry(func() error {
		return r.DB().Model(&model.AgentRun{}).Where("id = ? AND status = ?", runID, RunRunning).Updates(map[string]any{
			"exec_total": total, "exec_success": success, "exec_failed": failed, "exec_skipped": skipped,
		}).Error
	})
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

// Called once on startup, when no run of this process can be live.
func (r *Repository) SweepStaleRuns() (int64, error) {
	res := r.db.Model(&model.AgentRun{}).Where("status = ?", RunRunning).Updates(map[string]any{
		"status": RunStopped, "error": RunInterruptedError, "ended_at": time.Now(),
	})
	return res.RowsAffected, res.Error
}

type RunRef struct {
	ID     uint   `json:"id"`
	Phase  string `json:"phase"`
	Status string `json:"status"`
}

func (r *Repository) RunningRuns(sessionID uint) ([]model.AgentRun, error) {
	var runs []model.AgentRun
	err := r.db.Where("session_id = ? AND status = ?", sessionID, RunRunning).Find(&runs).Error
	return runs, err
}

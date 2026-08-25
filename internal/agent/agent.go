package agent

import (
	"context"
	"fmt"
	"sync"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

type fileBatch struct {
	files    []db.FileEntry
	batchIdx int32
}

type Agent struct {
	repo          *db.Repository
	fsCfg         config.RemoteFSConfig
	modelProvider *db.ModelProvider
	session       *db.ScanSession
	logger        *Logger
	sessionID     uint

	mu       sync.Mutex
	running  bool
	cancelFn context.CancelFunc
}

func New(repo *db.Repository, fsCfg config.RemoteFSConfig, cfg *config.Config, sessionID uint, modelProvider *db.ModelProvider, session *db.ScanSession) *Agent {
	return &Agent{
		repo:          repo,
		fsCfg:         fsCfg,
		modelProvider: modelProvider,
		session:       session,
		sessionID:     sessionID,
		logger:        NewLogger(repo, sessionID),
	}
}

func (a *Agent) GetLogger() *Logger {
	return a.logger
}

func (a *Agent) IsRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

func (a *Agent) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelFn != nil {
		a.cancelFn()
	}
	a.running = false
}

// RunTagging delegates to AutoTagAgent for automatic file classification.
// On success it returns the summary report for the run_done event.
func (a *Agent) RunTagging(ctx context.Context) (string, error) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return "", fmt.Errorf("agent is already running")
	}
	a.running = true
	ctx, cancel := context.WithCancel(ctx)
	a.cancelFn = cancel
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.running = false
		a.cancelFn = nil
		a.mu.Unlock()
		if ctx.Err() != nil {
			if s, err := a.repo.GetSession(a.sessionID); err == nil && s.Status == "tagging" {
				s.Status = "tagged"
				a.repo.UpdateSession(s)
			}
		}
	}()

	orch := NewOrchestratorAgent(a.repo, a.fsCfg, a.modelProvider, a.session, a.sessionID, a.logger)
	return orch.Run(ctx, OrchestratorRequest{Type: RequestAutoTag})
}

// RunInstruct delegates to InstructAgent for user-directed operations.
func (a *Agent) RunInstruct(ctx context.Context, files []db.FileEntry, userPrompt string) (string, error) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return "", fmt.Errorf("agent is already running")
	}
	a.running = true
	ctx, cancel := context.WithCancel(ctx)
	a.cancelFn = cancel
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.running = false
		a.cancelFn = nil
		a.mu.Unlock()
	}()

	orch := NewOrchestratorAgent(a.repo, a.fsCfg, a.modelProvider, a.session, a.sessionID, a.logger)
	return orch.Run(ctx, OrchestratorRequest{
		Type:       RequestInstruct,
		UserPrompt: userPrompt,
		Files:      files,
	})
}

// Legacy helpers kept for backward compatibility.

// RunTaggingDirect runs the auto-tagging pipeline directly without orchestration.
func (a *Agent) RunTaggingDirect(ctx context.Context) error {
	autoTag := NewAutoTagAgent(a.repo, a.fsCfg, a.modelProvider, a.session, a.sessionID)
	return autoTag.Run(ctx)
}

// RunInstructDirect runs the instruct pipeline directly without orchestration.
func (a *Agent) RunInstructDirect(ctx context.Context, files []db.FileEntry, userPrompt string) (string, error) {
	instruct := NewInstructAgent(a.repo, a.fsCfg, a.modelProvider, a.session, a.sessionID, a.logger)
	return instruct.Run(ctx, files, userPrompt)
}

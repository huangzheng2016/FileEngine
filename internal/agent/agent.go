package agent

import (
	"context"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

type fileBatch struct {
	files    []db.FileEntry
	batchIdx int32
}

// Agent runs the agent pipelines of one session. Run lifecycle (busy check,
// stop, terminal status, token totals) belongs to the caller's AgentRun;
// every event of a pipeline goes through the run's Logger.
type Agent struct {
	repo          *db.Repository
	fsCfg         config.RemoteFSConfig
	modelProvider *db.ModelProvider
	session       *db.ScanSession
	sessionID     uint
}

func New(repo *db.Repository, fsCfg config.RemoteFSConfig, sessionID uint, modelProvider *db.ModelProvider, session *db.ScanSession) *Agent {
	return &Agent{
		repo:          repo,
		fsCfg:         fsCfg,
		modelProvider: modelProvider,
		session:       session,
		sessionID:     sessionID,
	}
}

// RunTagging runs automatic classification and returns the tagging report;
// partial is true when some batches failed after retries.
func (a *Agent) RunTagging(ctx context.Context, logger *Logger) (report string, partial bool, err error) {
	orch := NewOrchestratorAgent(a.repo, a.fsCfg, a.modelProvider, a.session, a.sessionID, logger)
	report, err = orch.Run(ctx, OrchestratorRequest{Type: RequestAutoTag})
	return report, orch.Partial(), err
}

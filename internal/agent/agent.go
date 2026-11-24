package agent

import (
	"context"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

type fileBatch struct {
	files    []model.FileEntry
	batchIdx int32
}

type Agent struct {
	repo          *db.Repository
	fsCfg         config.RemoteFSConfig
	modelProvider *model.ModelProvider
	session       *model.ScanSession
	sessionID     uint
}

func New(repo *db.Repository, fsCfg config.RemoteFSConfig, sessionID uint, modelProvider *model.ModelProvider, session *model.ScanSession) *Agent {
	return &Agent{
		repo:          repo,
		fsCfg:         fsCfg,
		modelProvider: modelProvider,
		session:       session,
		sessionID:     sessionID,
	}
}

func (a *Agent) RunTagging(ctx context.Context, logger *Logger) (report string, partial bool, err error) {
	orch := NewOrchestratorAgent(a.repo, a.fsCfg, a.modelProvider, a.session, a.sessionID, logger)
	report, err = orch.Run(ctx, OrchestratorRequest{Type: RequestAutoTag})
	return report, orch.Partial(), err
}

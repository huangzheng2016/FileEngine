// Package sessionagent resolves a session's filesystem and model into a
// ready agent, for the services that start agent runs (plan drafting, chat).
package sessionagent

import (
	"context"
	"errors"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/errs"
	"FileEngine/internal/agent"
	"FileEngine/internal/config"
	"FileEngine/internal/db"

	"gorm.io/gorm"
)

var (
	errNoFilesystem = errs.Invalid("整理任务所用的存储已删除")
	errNoModel      = errs.Invalid("整理任务没有选模型")
	errModelDeleted = errs.Invalid("整理任务所选的模型已删除")
)

type Factory struct {
	q    *query.Query
	repo *db.Repository
}

func NewFactory(q *query.Query, repo *db.Repository) *Factory { return &Factory{q: q, repo: repo} }

// Session loads a session or answers 404.
func (f *Factory) Session(ctx context.Context, id uint) (*model.ScanSession, error) {
	s := f.q.ScanSession
	row, err := s.WithContext(ctx).Where(s.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errs.NoSession
	}
	return row, err
}

// FSConfig is the connection config of the session's filesystem.
func (f *Factory) FSConfig(ctx context.Context, session *model.ScanSession) (config.RemoteFSConfig, error) {
	if session.FilesystemID == 0 {
		return config.RemoteFSConfig{}, errNoFilesystem
	}
	fs := f.q.Filesystem
	row, err := fs.WithContext(ctx).Where(fs.ID.Eq(session.FilesystemID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return config.RemoteFSConfig{}, errNoFilesystem
	}
	if err != nil {
		return config.RemoteFSConfig{}, err
	}
	return row.ToRemoteFSConfig(), nil
}

// Agent builds the session's agent; a deleted model or filesystem is a 400.
func (f *Factory) Agent(ctx context.Context, session *model.ScanSession) (*agent.Agent, error) {
	if session.ModelProviderID == 0 {
		return nil, errNoModel
	}
	mp := f.q.ModelProvider
	provider, err := mp.WithContext(ctx).Where(mp.ID.Eq(session.ModelProviderID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errModelDeleted
	}
	if err != nil {
		return nil, err
	}
	fsCfg, err := f.FSConfig(ctx, session)
	if err != nil {
		return nil, err
	}
	return agent.New(f.repo, fsCfg, session.ID, provider, session), nil
}

// StartError maps a refused run start (the session already has an active run)
// to a 409; anything else is an internal error.
func StartError(err error) error {
	if errors.Is(err, db.ErrSessionBusy) {
		return errs.Conflict(err.Error())
	}
	return err
}

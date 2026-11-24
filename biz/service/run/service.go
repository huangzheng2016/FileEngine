// Package run starts and stops the work of a session: scans, tagging and review
// runs, and the agent and storage a run needs. It sits on the runner, which owns
// goroutines and shutdown; other services (plan, chat) launch their runs through
// it too.
package run

import (
	"context"
	"errors"
	"log/slog"
	"path"
	"strings"
	"sync"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/errs"
	"FileEngine/biz/service/runner"
	"FileEngine/internal/agent"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
	"FileEngine/internal/scanner"

	"gorm.io/gorm"
)

var (
	ErrNoRun        = errs.NotFound("运行不存在")
	errNoFilesystem = errs.Invalid("整理任务所用的存储已删除")
	errNoModel      = errs.Invalid("整理任务没有选模型")
	errModelGone    = errs.Invalid("整理任务所选的模型已删除")
	errFSConnect    = errs.Unavailable("无法连接存储")
	errScanning     = errs.Conflict("扫描还没结束")
	errScanningAuto = errs.Conflict("扫描还没结束，扫描完成后会自动开始整理")
)

type Service struct {
	q      *query.Query
	repo   *db.Repository
	runner *runner.Service

	mu sync.Mutex
	// sessions whose running scan was started with auto_tag; only used to word
	// the answer to a tagging request that arrives too early
	autoTag map[uint]bool
}

func NewService(q *query.Query, repo *db.Repository, r *runner.Service) *Service {
	return &Service{q: q, repo: repo, runner: r, autoTag: make(map[uint]bool)}
}

// Launch records a run and executes work on a detached context; see
// runner.Service.Launch. Start errors are already client errors.
func (s *Service) Launch(sessionID uint, phase, title string, work func(ctx context.Context, run *model.AgentRun) runner.Outcome) (*model.AgentRun, error) {
	run, err := s.runner.Launch(sessionID, phase, title, work)
	return run, StartError(err)
}

// StartError maps the errors of starting a run to the errors a client sees.
func StartError(err error) error {
	switch {
	case errors.Is(err, db.ErrSessionBusy), errors.Is(err, db.ErrAlreadyReverted), errors.Is(err, db.ErrRevertOfRevert):
		return errs.Conflict(err.Error())
	}
	return err
}

// Session loads a session; a missing one is errs.NoSession.
func (s *Service) Session(ctx context.Context, id uint) (*model.ScanSession, error) {
	sess, err := s.q.ScanSession.WithContext(ctx).Where(s.q.ScanSession.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errs.NoSession
	}
	return sess, err
}

// ResolveFSConfig is the connection of the session's storage.
func (s *Service) ResolveFSConfig(ctx context.Context, session *model.ScanSession) (config.RemoteFSConfig, error) {
	if session.FilesystemID == 0 {
		return config.RemoteFSConfig{}, errNoFilesystem
	}
	f := s.q.Filesystem
	fs, err := f.WithContext(ctx).Where(f.ID.Eq(session.FilesystemID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return config.RemoteFSConfig{}, errNoFilesystem
	}
	if err != nil {
		return config.RemoteFSConfig{}, err
	}
	return fs.ToRemoteFSConfig(), nil
}

// SessionFS connects to the session's storage. The caller closes it.
func (s *Service) SessionFS(ctx context.Context, sessionID uint) (remotefs.RemoteFS, error) {
	session, err := s.Session(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return s.connect(ctx, session)
}

func (s *Service) connect(ctx context.Context, session *model.ScanSession) (remotefs.RemoteFS, error) {
	cfg, err := s.ResolveFSConfig(ctx, session)
	if err != nil {
		return nil, err
	}
	fs, err := remotefs.NewFromConfig(cfg)
	if err != nil {
		slog.Warn("connect filesystem", "session", session.ID, "err", err)
		return nil, errFSConnect
	}
	return fs, nil
}

// AgentFor builds the agent of a session with its model and storage.
func (s *Service) AgentFor(ctx context.Context, session *model.ScanSession) (*agent.Agent, error) {
	if session.ModelProviderID == 0 {
		return nil, errNoModel
	}
	m := s.q.ModelProvider
	provider, err := m.WithContext(ctx).Where(m.ID.Eq(session.ModelProviderID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errModelGone
	}
	if err != nil {
		return nil, err
	}
	cfg, err := s.ResolveFSConfig(ctx, session)
	if err != nil {
		return nil, err
	}
	return agent.New(s.repo, cfg, session.ID, provider, session), nil
}

// AgentForSession loads the session and builds its agent.
func (s *Service) AgentForSession(ctx context.Context, sessionID uint) (*model.ScanSession, *agent.Agent, error) {
	session, err := s.Session(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	a, err := s.AgentFor(ctx, session)
	if err != nil {
		return nil, nil, err
	}
	return session, a, nil
}

// StartTagging starts the tagging run of a session whose scan has finished; a
// finished tagging run is followed by a review run.
func (s *Service) StartTagging(ctx context.Context, sessionID uint) (*model.AgentRun, error) {
	session, a, err := s.AgentForSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.Status == "scanning" {
		s.mu.Lock()
		auto := s.autoTag[sessionID]
		s.mu.Unlock()
		if auto {
			return nil, errScanningAuto
		}
		return nil, errScanning
	}
	return s.launchTagging(sessionID, a)
}

func (s *Service) launchTagging(sessionID uint, a *agent.Agent) (*model.AgentRun, error) {
	return s.Launch(sessionID, db.RunPhaseTagging, "整理", func(ctx context.Context, run *model.AgentRun) runner.Outcome {
		logger := agent.NewLogger(sessionID, run.ID, 0)
		report, partial, err := a.RunTagging(ctx, logger)
		return runner.Outcome{Summary: report, Partial: partial, Err: err, Tokens: logger.Tokens(),
			Then: func(status string) { s.startReviewAfterTagging(sessionID, run.ID, status) }}
	})
}

func (s *Service) startReviewAfterTagging(sessionID, taggingRunID uint, status string) {
	if status != db.RunDone && status != db.RunPartial {
		return
	}
	// Runs on the finished run's goroutine, after the process may be shutting down.
	ctx := context.Background()
	_, a, err := s.AgentForSession(ctx, sessionID)
	if err == nil {
		var tagging *model.AgentRun
		if tagging, err = s.repo.GetRun(taggingRunID); err == nil {
			_, err = s.Launch(sessionID, db.RunPhaseReview, "复查", func(ctx context.Context, run *model.AgentRun) runner.Outcome {
				logger := agent.NewLogger(sessionID, run.ID, 0)
				report, err := a.RunReview(ctx, logger, tagging)
				return runner.Outcome{Summary: report, Err: err, Tokens: logger.Tokens()}
			})
		}
	}
	if err != nil {
		slog.Warn("start review after tagging", "session", sessionID, "run", taggingRunID, "err", err)
	}
}

// ScanRoot is the normalized scan path: slash-separated, no leading or
// trailing slash, "" for the storage root.
func ScanRoot(scanPath string) string {
	return strings.Trim(path.Clean("/"+scanPath), "/")
}

// StartScan scans the session on a detached context; it returns once the scan
// is registered. With autoTag a tagging run follows a completed scan. The
// session row must already be scanning.
func (s *Service) StartScan(ctx context.Context, session *model.ScanSession, autoTag bool) error {
	cfg, err := s.ResolveFSConfig(ctx, session)
	if err != nil {
		return err
	}
	// The scan works on a copy: callers keep serializing their own struct.
	sess := *session
	filterMode := sess.FilterMode
	if filterMode == "" {
		filterMode = "blacklist"
	}
	var filterDirs []string
	for _, line := range strings.Split(sess.FilterDirs, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			filterDirs = append(filterDirs, line)
		}
	}
	if sess.ExcludeCategoryDirs && filterMode == "blacklist" {
		c := s.q.Category
		cats, err := c.WithContext(ctx).Where(c.FilesystemID.Eq(sess.FilesystemID)).Find()
		if err != nil {
			return err
		}
		for _, cat := range cats {
			if cat.Path != "" {
				filterDirs = append(filterDirs, cat.Path)
			}
		}
	}

	s.mu.Lock()
	s.autoTag[sess.ID] = autoTag
	s.mu.Unlock()
	s.runner.StartScan(sess.ID, func(ctx context.Context) {
		defer func() {
			s.mu.Lock()
			delete(s.autoTag, sess.ID)
			s.mu.Unlock()
		}()
		fs, err := remotefs.NewFromConfig(cfg)
		if err != nil {
			slog.Warn("scan: connect filesystem", "session", sess.ID, "err", err)
			s.failScan(sess.ID, err)
			return
		}
		defer fs.Close()
		if err := scanner.New(fs, s.repo, filterMode, filterDirs).Scan(ctx, &sess); err != nil {
			if ctx.Err() != nil {
				// Whoever cancelled owns the session state; an error written here
				// would race a fresh rescan's "scanning".
				slog.Info("scan cancelled", "session", sess.ID)
				return
			}
			slog.Warn("scan failed", "session", sess.ID, "err", err)
			s.failScan(sess.ID, err)
			return
		}
		if autoTag {
			if _, err := s.StartTagging(context.WithoutCancel(ctx), sess.ID); err != nil {
				slog.Warn("auto tag after scan", "session", sess.ID, "err", err)
			}
		}
	})
	return nil
}

func (s *Service) failScan(sessionID uint, err error) {
	if ferr := s.repo.FailScan(sessionID, err.Error()); ferr != nil {
		slog.Error("record scan failure", "session", sessionID, "err", ferr)
	}
}

// StopSession cancels a session's scan and runs and waits for them, so no write
// of theirs lands after the caller's own.
func (s *Service) StopSession(sessionID uint) {
	if !s.runner.CancelScan(sessionID) {
		slog.Warn("scan did not stop in time", "session", sessionID)
	}
	s.runner.StopSession(sessionID)
}

// StopScan cancels a session's scan and waits for its last writes.
func (s *Service) StopScan(sessionID uint) {
	if !s.runner.CancelScan(sessionID) {
		slog.Warn("scan did not stop in time", "session", sessionID)
	}
}

// List returns a session's runs, newest first.
func (s *Service) List(ctx context.Context, sessionID uint) ([]model.AgentRun, error) {
	if _, err := s.Session(ctx, sessionID); err != nil {
		return nil, err
	}
	return s.repo.ListRuns(sessionID)
}

// Get returns one run.
func (s *Service) Get(ctx context.Context, id uint) (*model.AgentRun, error) {
	run, err := s.repo.GetRun(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoRun
	}
	return run, err
}

// Stop cancels a running run and returns its final row; a finished run is
// returned as it is.
func (s *Service) Stop(ctx context.Context, id uint) (*model.AgentRun, error) {
	run, err := s.runner.Stop(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoRun
	}
	return run, err
}

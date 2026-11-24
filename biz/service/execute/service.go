// Package execute starts the runs that change the storage (execute, revert),
// checks a plan before it is executed, and finds entries by name or path.
package execute

import (
	"context"
	"errors"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/errs"
	"FileEngine/biz/service/run"
	"FileEngine/biz/service/runner"
	"FileEngine/internal/db"
	"FileEngine/internal/executor"

	"gorm.io/gorm"
)

const locateLimit = 50

var (
	errMode     = errs.Invalid("执行模式只能是 copy 或 move")
	errSample   = errs.Invalid("抽样数量不能为负数")
	errNoQuery  = errs.Invalid("请输入要查找的文件名或路径")
	errNotExec  = errs.Invalid(executor.ErrNotExecuting.Error())
	errNoTarget = errs.NotFound("运行不存在")
)

type Service struct {
	repo   *db.Repository
	run    *run.Service
	runner *runner.Service
}

func NewService(repo *db.Repository, r *run.Service, rn *runner.Service) *Service {
	return &Service{repo: repo, run: r, runner: rn}
}

type Options struct {
	// copy | move; empty means copy where a default is allowed
	Mode         string
	Sample       int
	CategoryPath string
}

func (o Options) validate() error {
	if o.Mode != executor.ModeCopy && o.Mode != executor.ModeMove {
		return errMode
	}
	if o.Sample < 0 {
		return errSample
	}
	return nil
}

func (o Options) executor() executor.Options {
	return executor.Options{Mode: o.Mode, Sample: o.Sample, CategoryPath: o.CategoryPath}
}

// Start begins an executing run over the session's planned entries. The run
// owns the storage connection and closes it when it ends.
func (s *Service) Start(ctx context.Context, sessionID uint, opts Options) (*model.AgentRun, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	fs, err := s.run.SessionFS(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	r, err := executor.Start(s.repo, fs, sessionID, opts.executor(), s.runner)
	if err != nil {
		fs.Close()
		return nil, run.StartError(err)
	}
	return r, nil
}

// Check validates what Start would do, without touching the storage. Entries
// skipped because an ancestor carries its own decision are counted as skipped
// but not listed: they are not a problem to fix.
func (s *Service) Check(ctx context.Context, sessionID uint, opts Options) (*executor.Result, error) {
	if opts.Mode == "" {
		opts.Mode = executor.ModeCopy
	}
	if err := opts.validate(); err != nil {
		return nil, err
	}
	fs, err := s.run.SessionFS(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	defer fs.Close()
	res, err := executor.New(s.repo, fs).Validate(ctx, sessionID, opts.executor())
	if err != nil {
		return nil, err
	}
	issues := res.Issues[:0:0]
	for _, i := range res.Issues {
		if i.Code != executor.IssueNested {
			issues = append(issues, i)
		}
	}
	res.Issues = issues
	return res, nil
}

// Revert begins a revert run undoing an executing run.
func (s *Service) Revert(ctx context.Context, runID uint) (*model.AgentRun, error) {
	target, err := s.run.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	switch {
	case target.Phase == db.RunPhaseRevert:
		return nil, run.StartError(db.ErrRevertOfRevert)
	case target.Phase != db.RunPhaseExecuting:
		return nil, errNotExec
	case target.RevertedByRunID != nil:
		return nil, run.StartError(db.ErrAlreadyReverted)
	}
	fs, err := s.run.SessionFS(ctx, target.SessionID)
	if err != nil {
		return nil, err
	}
	r, err := executor.StartRevert(s.repo, fs, runID, s.runner)
	if err != nil {
		fs.Close()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNoTarget
		}
		if errors.Is(err, executor.ErrNotExecuting) {
			return nil, errNotExec
		}
		return nil, run.StartError(err)
	}
	return r, nil
}

// Locate finds the session's entries whose name or path contains q, with where
// each ends up.
func (s *Service) Locate(ctx context.Context, sessionID uint, q string) ([]executor.LocateItem, error) {
	if q == "" {
		return nil, errNoQuery
	}
	if _, err := s.run.Session(ctx, sessionID); err != nil {
		return nil, err
	}
	return executor.Locate(s.repo, sessionID, q, locateLimit)
}

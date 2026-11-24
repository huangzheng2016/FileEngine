// Package rule manages the session's sorting rules and applies them.
package rule

import (
	"context"
	"errors"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/errs"
	"FileEngine/biz/service/sessionagent"
	"FileEngine/internal/db"
)

var errNoRule = errs.NotFound("规则不存在")

type Service struct {
	repo *db.Repository
	sa   *sessionagent.Factory
}

func NewService(repo *db.Repository, sa *sessionagent.Factory) *Service {
	return &Service{repo: repo, sa: sa}
}

// Patch holds the fields of a create or update; nil keeps the current value
// (empty on create).
type Patch struct {
	Name, Exts, NameRegex, PathPrefix, FileType, Target, Role, Source *string
	MinSize, MaxSize                                                  *int64
	Position                                                          *int
}

func (p *Patch) apply(r *model.PlanRule) {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&r.Name, p.Name)
	set(&r.Exts, p.Exts)
	set(&r.NameRegex, p.NameRegex)
	set(&r.PathPrefix, p.PathPrefix)
	set(&r.FileType, p.FileType)
	set(&r.Target, p.Target)
	set(&r.Role, p.Role)
	set(&r.Source, p.Source)
	if p.MinSize != nil {
		r.MinSize = *p.MinSize
	}
	if p.MaxSize != nil {
		r.MaxSize = *p.MaxSize
	}
	if p.Position != nil {
		r.Position = *p.Position
	}
}

func (p *Patch) validSource() bool {
	if p.Source == nil {
		return true
	}
	switch *p.Source {
	case db.MemorySourceUser, "agent", "preset":
		return true
	}
	return false
}

func (s *Service) List(ctx context.Context, sessionID uint) ([]model.PlanRule, error) {
	if _, err := s.sa.Session(ctx, sessionID); err != nil {
		return nil, err
	}
	return s.repo.ListRules(sessionID)
}

func (s *Service) Create(ctx context.Context, sessionID uint, p Patch) (*model.PlanRule, error) {
	if _, err := s.sa.Session(ctx, sessionID); err != nil {
		return nil, err
	}
	if !p.validSource() {
		return nil, errs.Invalid("规则来源只能是 user、agent 或 preset")
	}
	rule := &model.PlanRule{SessionID: sessionID, Source: db.MemorySourceUser}
	p.apply(rule)
	if err := s.repo.CreateRule(rule); err != nil {
		return nil, translate(err)
	}
	return rule, nil
}

func (s *Service) Update(ctx context.Context, id uint, p Patch) (*model.PlanRule, error) {
	if !p.validSource() {
		return nil, errs.Invalid("规则来源只能是 user、agent 或 preset")
	}
	rule, err := s.repo.UpdateRule(id, p.apply)
	if err != nil {
		return nil, translate(err)
	}
	return rule, nil
}

// Delete removes a rule; one that is already gone is a success.
func (s *Service) Delete(ctx context.Context, id uint) error {
	if err := s.repo.DeleteRule(id); err != nil && !errors.Is(err, db.ErrRuleNotFound) {
		return err
	}
	return nil
}

type Result struct {
	Stats   []db.RuleApplyStat
	Applied int
	Skipped int
}

// Apply runs the rules over the session; with preview it only reports matches.
func (s *Service) Apply(ctx context.Context, sessionID uint, preview bool) (*Result, error) {
	if _, err := s.sa.Session(ctx, sessionID); err != nil {
		return nil, err
	}
	stats, err := s.repo.ApplyRules(sessionID, 0, preview)
	if err != nil {
		return nil, translate(err)
	}
	out := &Result{Stats: stats}
	for _, st := range stats {
		out.Applied += st.Matched
		out.Skipped += st.Skipped
	}
	return out, nil
}

func translate(err error) error {
	var rej *db.Rejection
	switch {
	case errors.Is(err, db.ErrRuleNotFound):
		return errNoRule
	case errors.As(err, &rej) && rej.Conflict:
		return errs.Conflict(rej.Msg)
	case errors.As(err, &rej):
		return errs.Invalid(rej.Msg)
	}
	return err
}

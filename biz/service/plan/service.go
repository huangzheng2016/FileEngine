// Package plan owns the session's written plan and the review of its decisions:
// the plan text, the dry-run operation list, the preview trees and confirming
// low-confidence decisions.
package plan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/errs"
	"FileEngine/biz/service/run"
	"FileEngine/biz/service/runner"
	"FileEngine/internal/agent"
	"FileEngine/internal/db"
)

// ReviewConfidence is the confidence below which a decision waits for the user.
const ReviewConfidence = db.ReviewConfidence

const (
	maxInstructionRunes = 4000
	maxResetIDs         = 5000
	previewDefaultLimit = 200
	previewMaxLimit     = 1000
)

var (
	errNoPlan    = errs.NotFound("还没有整理方案")
	errStalePlan = errs.Conflict("方案已被其他人修改，请刷新后重试")
)

type Service struct {
	q    *query.Query
	repo *db.Repository
	runs *run.Service
}

func NewService(q *query.Query, repo *db.Repository, runs *run.Service) *Service {
	return &Service{q: q, repo: repo, runs: runs}
}

// Get returns the session's plan; a session without one gets version 0.
func (s *Service) Get(ctx context.Context, sessionID uint) (*model.SessionPlan, error) {
	if _, err := s.runs.Session(ctx, sessionID); err != nil {
		return nil, err
	}
	p := s.q.SessionPlan
	rows, err := p.WithContext(ctx).Where(p.SessionID.Eq(sessionID)).Limit(1).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &model.SessionPlan{SessionID: sessionID}, nil
	}
	return rows[0], nil
}

// Update saves the editor's text; a save from a stale version is a conflict.
func (s *Service) Update(ctx context.Context, sessionID uint, content string, version int) (*model.SessionPlan, error) {
	if _, err := s.runs.Session(ctx, sessionID); err != nil {
		return nil, err
	}
	plan, err := s.repo.SavePlan(sessionID, content, version, "")
	if errors.Is(err, db.ErrPlanVersionConflict) {
		return nil, errStalePlan
	}
	if err != nil {
		return nil, err
	}
	db.Emit(sessionID, 0, db.EvPlanUpdated, db.PlanUpdatedData{Version: plan.Version, Status: plan.Status})
	return plan, nil
}

func (s *Service) Approve(ctx context.Context, sessionID uint) (*model.SessionPlan, error) {
	if _, err := s.runs.Session(ctx, sessionID); err != nil {
		return nil, err
	}
	plan, err := s.repo.ApprovePlan(sessionID)
	if errors.Is(err, db.ErrPlanNotFound) {
		return nil, errNoPlan
	}
	if err != nil {
		return nil, err
	}
	db.Emit(sessionID, 0, db.EvPlanUpdated, db.PlanUpdatedData{Version: plan.Version, Status: plan.Status})
	return plan, nil
}

// Draft starts a survey run that writes the plan and returns its run id.
func (s *Service) Draft(ctx context.Context, sessionID uint, instructions string) (uint, error) {
	if len([]rune(instructions)) > maxInstructionRunes {
		return 0, errs.Invalid("补充说明过长")
	}
	session, err := s.runs.Session(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	if session.Status == "scanning" {
		return 0, errs.Conflict("整理任务还在扫描，扫描完成后再生成方案")
	}
	a, err := s.runs.AgentFor(ctx, session)
	if err != nil {
		return 0, err
	}
	launched, err := s.runs.Launch(sessionID, db.RunPhaseSurvey, "生成方案", func(ctx context.Context, run *model.AgentRun) runner.Outcome {
		logger := agent.NewLogger(sessionID, run.ID, 0)
		summary, err := a.RunSurvey(ctx, logger, instructions)
		return runner.Outcome{Summary: summary, Err: err, Tokens: logger.Tokens()}
	})
	if err != nil {
		return 0, err
	}
	return launched.ID, nil
}

type Op struct {
	FileID                                       uint
	OriginalPath, NewPath, Operation, Name, Type string
}

// Ops is the dry-run list of the decided, unexecuted entries.
func (s *Service) Ops(ctx context.Context, sessionID uint) ([]Op, error) {
	if _, err := s.runs.Session(ctx, sessionID); err != nil {
		return nil, err
	}
	files, err := s.repo.ListPlannedEntries(sessionID)
	if err != nil {
		return nil, err
	}
	ops := make([]Op, len(files))
	for i, f := range files {
		ops[i] = Op{FileID: f.ID, OriginalPath: f.OriginalPath, NewPath: f.NewPath, Operation: f.Operation, Name: f.Name, Type: f.FileType}
	}
	return ops, nil
}

// ExportCSV is Ops as a UTF-8 CSV with a BOM, for spreadsheet programs.
func (s *Service) ExportCSV(ctx context.Context, sessionID uint) ([]byte, error) {
	ops, err := s.Ops(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("\xEF\xBB\xBF")
	b.WriteString("original_path,new_path,operation,file_type,name\n")
	for _, o := range ops {
		fmt.Fprintf(&b, "%s,%s,%s,%s,%s\n", csvEscape(o.OriginalPath), csvEscape(o.NewPath), o.Operation, o.Type, csvEscape(o.Name))
	}
	return b.Bytes(), nil
}

// csvEscape quotes a field and neutralizes spreadsheet formulas.
func csvEscape(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		v = "'" + v
	}
	if strings.ContainsAny(v, ",\"\n\r") || strings.HasPrefix(v, "'") {
		return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
	}
	return v
}

// Reset clears the decisions of the given entries and returns how many it reset.
func (s *Service) Reset(ctx context.Context, sessionID uint, ids []uint) (int64, error) {
	if len(ids) > maxResetIDs {
		return 0, errs.BadRequest
	}
	if _, err := s.runs.Session(ctx, sessionID); err != nil {
		return 0, err
	}
	return s.repo.ResetPlans(sessionID, ids)
}

// Confirm accepts low-confidence decisions so they run with the plan.
func (s *Service) Confirm(ctx context.Context, sessionID uint, ids []uint) (int64, error) {
	if _, err := s.runs.Session(ctx, sessionID); err != nil {
		return 0, err
	}
	return s.repo.ConfirmReview(sessionID, ids)
}

// Preview browses the virtual target tree (side "after") or the scanned tree
// with a status per item (side "before").
func (s *Service) Preview(ctx context.Context, sessionID uint, side, dir string, offset, limit int) (*db.PreviewPage, error) {
	session, err := s.runs.Session(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > previewMaxLimit {
		limit = previewDefaultLimit
	}
	switch side {
	case "", "after":
		return s.repo.PreviewAfter(sessionID, dir, offset, limit)
	case "before":
		return s.repo.PreviewBefore(sessionID, session.RootPath, dir, offset, limit)
	}
	return nil, errs.Invalid("side 只能是 after 或 before")
}

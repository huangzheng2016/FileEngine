// Package event reads a session's persisted events and resolves where a stream
// of them starts. Delivery itself (replay plus live broadcast) is eventfeed's.
package event

import (
	"context"
	"strconv"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/errs"
	"FileEngine/biz/service/eventfeed"
	"FileEngine/biz/service/run"
	"FileEngine/internal/db"
)

type Service struct {
	repo *db.Repository
	run  *run.Service
}

func NewService(repo *db.Repository, r *run.Service) *Service {
	return &Service{repo: repo, run: r}
}

// ListQuery selects a page of events: After and Before are exclusive seq bounds
// (0 = unbounded) and Limit 0 means the default page.
type ListQuery struct {
	SessionID, RunID, After, Before uint
	Limit                           int
}

// List returns events in ascending seq order and whether more exist beyond the
// page.
func (s *Service) List(_ context.Context, q ListQuery) ([]model.SessionEvent, bool, error) {
	if q.Limit < 0 {
		return nil, false, errs.BadRequest
	}
	return s.repo.ListEvents(db.EventQuery{SessionID: q.SessionID, RunID: q.RunID, After: q.After, Before: q.Before, Limit: q.Limit})
}

// StreamStart checks the session exists and resolves the seq a stream starts
// after: the resume point (Last-Event-ID or `after`) when the client has one,
// else the first event of the session's latest run.
func (s *Service) StreamStart(ctx context.Context, sessionID, runID uint, resume string) (uint, error) {
	if _, err := s.run.Session(ctx, sessionID); err != nil {
		return 0, err
	}
	if resume != "" {
		n, err := strconv.ParseUint(resume, 10, 64)
		if err != nil {
			return 0, errs.Invalid("Last-Event-ID 无效")
		}
		return uint(n), nil
	}
	return eventfeed.StartSeq(s.repo, sessionID, runID)
}

// Follow delivers the session's events after the given seq to sink until ctx
// ends or the sink fails.
func (s *Service) Follow(ctx context.Context, q eventfeed.Query, sink eventfeed.Sink) error {
	return eventfeed.Follow(ctx, s.repo, q, sink)
}

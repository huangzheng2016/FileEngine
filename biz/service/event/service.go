// Package event reads a session's persisted events and resolves where a stream
// of them starts. Delivery itself (replay plus live broadcast) is eventfeed's.
package event

import (
	"context"
	"strconv"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/errs"
	"FileEngine/biz/service/eventfeed"
	"FileEngine/biz/service/run"
)

type Service struct {
	rq  *dal.Reader
	run *run.Service
}

func NewService(rq *dal.Reader, r *run.Service) *Service {
	return &Service{rq: rq, run: r}
}

// ListQuery selects a page of events: After and Before are exclusive seq bounds
// (0 = unbounded) and Limit 0 means the default page.
type ListQuery = eventfeed.ListQuery

// List returns events in ascending seq order and whether more exist beyond the
// page.
func (s *Service) List(ctx context.Context, q ListQuery) ([]model.SessionEvent, bool, error) {
	if q.Limit < 0 {
		return nil, false, errs.BadRequest
	}
	return eventfeed.List(ctx, s.rq.Query, q)
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
	return eventfeed.StartSeq(ctx, s.rq.Query, sessionID, runID)
}

// Follow delivers the session's events after the given seq to sink until ctx
// ends or the sink fails.
func (s *Service) Follow(ctx context.Context, q eventfeed.Query, sink eventfeed.Sink) error {
	return eventfeed.Follow(ctx, s.rq.Query, q, sink)
}

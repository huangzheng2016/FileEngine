package eventfeed

import (
	"context"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
)

const (
	defaultPage = 500
	maxPage     = 1000
)

// ListQuery selects a page of a session's events; After and Before are
// exclusive seq bounds (0 = unbounded).
type ListQuery struct {
	SessionID uint
	RunID     uint // 0 = all runs
	After     uint
	Before    uint
	Limit     int
}

// List returns events in ascending seq order and whether more exist beyond the
// page. With only Before set it returns the newest events below Before.
func List(ctx context.Context, q *query.Query, lq ListQuery) ([]model.SessionEvent, bool, error) {
	if lq.Limit <= 0 || lq.Limit > maxPage {
		lq.Limit = defaultPage
	}
	e := q.SessionEvent
	do := e.WithContext(ctx).Where(e.SessionID.Eq(lq.SessionID))
	if lq.RunID > 0 {
		do = do.Where(e.RunID.Eq(lq.RunID))
	}
	if lq.After > 0 {
		do = do.Where(e.ID.Gt(lq.After))
	}
	if lq.Before > 0 {
		do = do.Where(e.ID.Lt(lq.Before))
	}
	desc := lq.Before > 0 && lq.After == 0
	if desc {
		do = do.Order(e.ID.Desc())
	} else {
		do = do.Order(e.ID)
	}
	ptrs, err := do.Limit(lq.Limit + 1).Find()
	if err != nil {
		return nil, false, err
	}
	more := len(ptrs) > lq.Limit
	if more {
		ptrs = ptrs[:lq.Limit]
	}
	if desc {
		for i, j := 0, len(ptrs)-1; i < j; i, j = i+1, j-1 {
			ptrs[i], ptrs[j] = ptrs[j], ptrs[i]
		}
	}
	rows := make([]model.SessionEvent, len(ptrs))
	for i, p := range ptrs {
		rows[i] = *p
	}
	return rows, more, nil
}

// latestRunStart is the seq just before the first event of the session's
// latest run, so a stream started there replays that whole run; 0 when the
// session has no run or the run has no events.
func latestRunStart(ctx context.Context, q *query.Query, sessionID uint) (uint, error) {
	r := q.AgentRun
	// Limit(1).Find: a session without runs is normal, not an error to log
	runs, err := r.WithContext(ctx).Select(r.ID).Where(r.SessionID.Eq(sessionID)).Order(r.ID.Desc()).Limit(1).Find()
	if err != nil {
		return 0, err
	}
	if len(runs) == 0 {
		return 0, nil
	}
	run := runs[0]
	e := q.SessionEvent
	var first struct{ Min *uint }
	if err := e.WithContext(ctx).Select(e.ID.Min().As("min")).Where(e.SessionID.Eq(sessionID), e.RunID.Eq(run.ID)).Scan(&first); err != nil {
		return 0, err
	}
	if first.Min == nil || *first.Min == 0 {
		return 0, nil
	}
	return *first.Min - 1, nil
}

// Package eventfeed delivers a session's event history and live broadcast to
// one consumer in order and without gaps. The SSE route and the desktop shell
// both follow through it, and one Registry cancels every follower at shutdown.
package eventfeed

import (
	"context"
	"errors"
	"time"

	"FileEngine/internal/db"
)

const replayPageSize = 500

// Sink receives events in strictly increasing seq order.
type Sink interface {
	Event(ev db.SessionEvent) error
	// Ping is called every Query.Heartbeat so a transport can notice a dead peer
	// by a failed write.
	Ping() error
}

type Query struct {
	SessionID uint
	// 0 = every run
	RunID uint
	// events with seq greater than After are delivered
	After uint
	// Heartbeat <= 0 disables pings.
	Heartbeat time.Duration
}

// StartSeq resolves where a follower without an explicit resume point begins:
// the first event of the session's latest run (or 0 when a run filter is set).
func StartSeq(repo *db.Repository, sessionID, runID uint) (uint, error) {
	if runID != 0 {
		return 0, nil
	}
	return repo.LatestRunStartSeq(sessionID)
}

// Follow subscribes first, replays the persisted events after q.After, then
// follows the live broadcast, dropping events it already replayed. A lagging
// subscriber re-reads the gap from the database. It returns when ctx ends
// (nil) or the sink or the database fails.
func Follow(ctx context.Context, repo *db.Repository, q Query, sink Sink) error {
	sub := db.Subscribe(q.SessionID)
	defer db.Unsubscribe(sub)

	last := q.After
	send := func(ev db.SessionEvent) error {
		if ev.ID <= last || (q.RunID > 0 && ev.RunID != q.RunID) {
			return nil
		}
		if err := sink.Event(ev); err != nil {
			return err
		}
		last = ev.ID
		return nil
	}
	replay := func() error {
		for {
			rows, more, err := repo.ListEvents(db.EventQuery{SessionID: q.SessionID, RunID: q.RunID, After: last, Limit: replayPageSize})
			if err != nil {
				return err
			}
			for _, ev := range rows {
				if err := send(ev); err != nil {
					return err
				}
			}
			if !more {
				return nil
			}
		}
	}
	if err := replay(); err != nil {
		return ignoreCancel(ctx, err)
	}

	var beat <-chan time.Time
	if q.Heartbeat > 0 {
		t := time.NewTicker(q.Heartbeat)
		defer t.Stop()
		beat = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-sub.C:
			if sub.TakeLagged() {
				if err := replay(); err != nil {
					return ignoreCancel(ctx, err)
				}
				continue
			}
			if err := send(ev); err != nil {
				return err
			}
		case <-beat:
			if sub.TakeLagged() {
				if err := replay(); err != nil {
					return ignoreCancel(ctx, err)
				}
			}
			if err := sink.Ping(); err != nil {
				return err
			}
		}
	}
}

func ignoreCancel(ctx context.Context, err error) error {
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}

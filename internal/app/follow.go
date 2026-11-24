package app

import (
	"context"
	"errors"

	"FileEngine/biz/errs"
	"FileEngine/biz/service/eventfeed"
	"FileEngine/container"
	"FileEngine/internal/db"

	"gorm.io/gorm"
)

type noPing struct{ send func(db.SessionEvent) error }

func (n noPing) Event(ev db.SessionEvent) error { return n.send(ev) }
func (noPing) Ping() error                      { return nil }

// Follow streams a session's events to send in order and without gaps: the
// history after `after` (nil = from the latest run's first event) and then the
// live broadcast, the same guarantee as the SSE route. It returns once the
// subscription is established. onEnd is called exactly once when following
// stops: nil after stop or shutdown, an error when send or the database failed.
// The follower is registered with the stream registry, so Shutdown cancels it.
func (a *App) Follow(sessionID, runID uint, after *uint, send func(db.SessionEvent) error, onEnd func(error)) (stop func(), err error) {
	c := container.Default
	if _, err := c.Repo.GetSession(sessionID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.NoSession
		}
		return nil, err
	}
	var from uint
	if after != nil {
		from = *after
	} else if from, err = eventfeed.StartSeq(c.Repo, sessionID, runID); err != nil {
		return nil, err
	}
	ctx, done, err := c.Streams.Begin()
	if err != nil {
		return nil, errs.Unavailable("服务正在关闭")
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer done()
		defer cancel()
		onEnd(eventfeed.Follow(ctx, c.Repo, eventfeed.Query{SessionID: sessionID, RunID: runID, After: from}, noPing{send}))
	}()
	return cancel, nil
}

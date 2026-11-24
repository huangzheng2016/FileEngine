package mw

import (
	"context"
	"sync"
	"time"

	"FileEngine/biz/errs"
	"FileEngine/biz/handler/resp"

	"github.com/cloudwego/hertz/pkg/app"
)

// RateLimit is a fixed-window budget shared by every request that goes through
// the returned handler. It is global rather than per client: a single-user app
// only needs to stop a leaked cookie or a runaway script from hammering login
// or the outbound test endpoints. Routes that share a budget share the handler.
func RateLimit(limit int, window time.Duration) app.HandlerFunc {
	var (
		mu    sync.Mutex
		start time.Time
		n     int
	)
	resets.Lock()
	resets.fns = append(resets.fns, func() {
		mu.Lock()
		start, n = time.Time{}, 0
		mu.Unlock()
	})
	resets.Unlock()
	return func(ctx context.Context, c *app.RequestContext) {
		mu.Lock()
		now := time.Now()
		if start.IsZero() || now.Sub(start) >= window {
			start, n = now, 0
		}
		allowed := n < limit
		if allowed {
			n++
		}
		mu.Unlock()
		if !allowed {
			resp.Abort(c, errs.TooManyRequests)
			return
		}
		c.Next(ctx)
	}
}

var resets struct {
	sync.Mutex
	fns []func()
}

// ResetRateLimits empties every budget. Budgets are package-level so routes can
// share one; tests that start several apps in one process reset them.
func ResetRateLimits() {
	resets.Lock()
	defer resets.Unlock()
	for _, f := range resets.fns {
		f()
	}
}

package mw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"runtime/debug"
	"time"

	"FileEngine/biz/errs"
	"FileEngine/biz/handler/resp"

	"github.com/cloudwego/hertz/pkg/app"
)

// AccessLog tags each request with an X-Request-Id (reusing the caller's) and
// logs one line per request. Path only, never the query string: the bootstrap
// URL carries the token.
func AccessLog() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		start := time.Now()
		id := string(c.GetHeader("X-Request-Id"))
		if id == "" {
			id = newRequestID()
		}
		c.Set("request_id", id)
		c.Header("X-Request-Id", id)
		c.Next(ctx)
		slog.Info("http", "method", string(c.Method()), "path", string(c.Path()),
			"status", c.Response.StatusCode(), "dur", time.Since(start).Round(time.Millisecond), "request_id", id)
	}
}

// Recover turns a handler panic into the standard 500 envelope and logs the stack.
func Recover() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		defer func() {
			if p := recover(); p != nil {
				slog.ErrorContext(ctx, "panic in handler", "request_id", c.GetString("request_id"),
					"path", string(c.Path()), "panic", p, "stack", string(debug.Stack()))
				resp.Abort(c, errs.Internal)
			}
		}()
		c.Next(ctx)
	}
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

package mw

import (
	"context"
	"strings"

	"FileEngine/biz/actor"
	"FileEngine/biz/errs"
	"FileEngine/biz/handler/resp"
	"FileEngine/biz/service/auth"
	"FileEngine/container"

	"github.com/cloudwego/hertz/pkg/app"
)

// publicRoutes are reachable without the access token ("METHOD /path" as
// registered). Every other /api route requires it; that is the point of putting
// Auth in rootMw instead of on individual routes.
var publicRoutes = map[string]bool{
	"POST /api/auth/login":    true,
	"GET /api/auth/bootstrap": true,
}

// Auth runs on every /api route. In-process desktop calls carry the actor
// marker and need no token; network requests need the bearer token or the
// session cookie.
func Auth() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if actor.Desktop(ctx) || publicRoutes[string(c.Method())+" "+c.FullPath()] {
			c.Next(ctx)
			return
		}
		if !container.Default.Auth.TokenMatches(RequestToken(c)) {
			resp.Abort(c, errs.NotLoggedIn)
			return
		}
		c.Next(ctx)
	}
}

// RequestToken is the bearer token, or else the session cookie. A malformed
// Authorization header never falls back to the cookie.
func RequestToken(c *app.RequestContext) string {
	if h := string(c.GetHeader("Authorization")); h != "" {
		if t, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(t)
		}
		return ""
	}
	return string(c.Cookie(auth.SessionCookie))
}

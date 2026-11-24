package mw

import (
	"context"
	"strings"

	"FileEngine/biz/actor"
	"FileEngine/biz/errs"
	"FileEngine/biz/handler/resp"
	"FileEngine/container"

	"github.com/cloudwego/hertz/pkg/app"
)

// There are deliberately no CORS headers: the SPA is served same-origin, so no
// cross-origin caller is ever granted access (and preflights fail).

const (
	DefaultBodyLimit = 1 << 20 // 1MB for every JSON body
	ImportBodyLimit  = 8 << 20 // category import
	importPath       = "/api/category/import"
)

// SecurityHeaders gives API responses a deny-all CSP (they are never documents);
// the SPA only forbids framing so its own scripts and styles keep working.
func SecurityHeaders() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(string(c.Path()), "/api/") {
			c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; sandbox")
		} else {
			c.Header("Content-Security-Policy", "frame-ancestors 'none'")
		}
		c.Next(ctx)
	}
}

// HostGuard rejects requests whose Host header is not ours (see
// auth.Service.HostAllowed).
func HostGuard() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if !actor.Desktop(ctx) && !container.Default.Auth.HostAllowed(string(c.Host())) {
			resp.Abort(c, errs.HostDenied)
			return
		}
		c.Next(ctx)
	}
}

// FetchMetadata rejects requests a browser marks as coming from another origin.
// Requests authenticated with an Authorization header are exempt: a
// cross-origin page cannot attach one without a CORS preflight, which this
// server never answers. Same-site is rejected too: another localhost port is
// same-site and would otherwise receive our SameSite=Strict cookie.
func FetchMetadata() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if actor.Desktop(ctx) || len(c.GetHeader("Authorization")) > 0 {
			c.Next(ctx)
			return
		}
		switch string(c.GetHeader("Sec-Fetch-Site")) {
		case "", "same-origin", "none":
		default:
			resp.Abort(c, errs.CrossSite)
			return
		}
		if d := string(c.GetHeader("Sec-Fetch-Dest")); d == "iframe" || d == "frame" {
			resp.Abort(c, errs.CrossSite)
			return
		}
		if origin := string(c.GetHeader("Origin")); origin != "" {
			if !strings.EqualFold(origin, string(c.URI().Scheme())+"://"+string(c.Host())) {
				resp.Abort(c, errs.CrossSite)
				return
			}
		}
		c.Next(ctx)
	}
}

// BodyLimit caps request bodies: 1 MB, 8 MB for category import. The server
// itself reads at most ImportBodyLimit.
func BodyLimit() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		limit := DefaultBodyLimit
		if string(c.Path()) == importPath {
			limit = ImportBodyLimit
		}
		if len(c.Request.Body()) > limit {
			resp.Abort(c, errs.TooLarge)
			return
		}
		c.Next(ctx)
	}
}

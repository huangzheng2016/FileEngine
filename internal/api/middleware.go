package api

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// There are deliberately no CORS headers: the SPA is served same-origin, so
// no cross-origin caller is ever granted access (and preflights fail).

const (
	defaultBodyLimit = 1 << 20 // 1MB for every JSON body
	importBodyLimit  = 8 << 20 // category import
)

// API responses get a deny-all CSP (they are never documents); the SPA only
// forbids framing so its own scripts and styles keep working.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; sandbox")
		} else {
			h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		}
		c.Next()
	}
}

func bodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil {
			c.Next()
			return
		}
		limit := int64(defaultBodyLimit)
		if c.Request.URL.Path == "/api/v1/categories/import" {
			limit = importBodyLimit
		}
		if c.Request.ContentLength > limit {
			abortError(c, http.StatusRequestEntityTooLarge, "请求体过大")
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// Path only, never the query string: the bootstrap URL carries the token.
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("http", "method", c.Request.Method, "path", c.Request.URL.Path,
			"status", c.Writer.Status(), "dur", time.Since(start).Round(time.Millisecond))
	}
}

// A single-user app, so one global budget is enough to stop a leaked cookie
// or a runaway script from hammering outbound test endpoints.
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*rateWindow
}

type rateWindow struct {
	start time.Time
	n     int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, hits: map[string]*rateWindow{}}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w := l.hits[key]
	if w == nil || now.Sub(w.start) >= l.window {
		l.hits[key] = &rateWindow{start: now, n: 1}
		return true
	}
	if w.n >= l.limit {
		return false
	}
	w.n++
	return true
}

func (s *Server) rateLimited(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.limiter.allow(key) {
			abortError(c, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
			return
		}
		c.Next()
	}
}

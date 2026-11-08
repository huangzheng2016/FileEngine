package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"FileEngine/internal/config"

	"github.com/gin-gonic/gin"
)

// One authentication mechanism for server and desktop mode: a single bearer
// token, accepted either as `Authorization: Bearer <token>` (scripts, CLI) or
// as the HttpOnly SameSite=Strict session cookie the SPA gets from
// POST /auth/login (server) or GET /auth/bootstrap (desktop window start
// URL). The cookie is what lets EventSource/fetch reach the SSE stream
// same-origin without exposing the token to page scripts.
const (
	SessionCookie = "fe_session"
	BootstrapPath = "/api/v1/auth/bootstrap"
	tokenParam    = "token"
)

type Options struct {
	// Empty falls back to config server.token, then to a random per-process
	// token: auth is never off.
	Token string
	// BindHost is the listen host. A loopback host enables the DNS-rebinding
	// guard: only localhost / 127.0.0.1 / ::1 Host headers are served.
	// Empty falls back to config server.host.
	BindHost string
	// AllowedHosts, when set, pins the Host header to these exact values
	// (desktop: the "127.0.0.1:<port>" of its listener) instead of the
	// loopback-name check.
	AllowedHosts []string
}

func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func resolveOptions(opts []Options) Options {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	cfg := config.Get()
	if o.Token == "" && cfg != nil {
		o.Token = cfg.Server.Token
	}
	if o.Token == "" {
		t, err := NewToken()
		if err != nil {
			panic("api: cannot generate token: " + err.Error())
		}
		o.Token = t
	}
	if o.BindHost == "" && cfg != nil {
		o.BindHost = cfg.Server.Host
	}
	return o
}

func (s *Server) tokenMatches(candidate string) bool {
	return candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(s.opts.Token)) == 1
}

func requestToken(r *http.Request) (string, bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		if t, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(t), true
		}
		return "", true
	}
	if c, err := r.Cookie(SessionCookie); err == nil {
		return c.Value, false
	}
	return "", false
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// hostGuard rejects requests whose Host header is not ours (DNS rebinding:
// a page on evil.example resolving to 127.0.0.1 would otherwise talk to us
// same-origin). Active only when bound to loopback or when exact hosts are
// pinned; a public bind serves any Host and relies on the token.
func (s *Server) hostGuard() gin.HandlerFunc {
	pinned := s.opts.AllowedHosts
	loopback := isLoopbackHost(strings.Trim(s.opts.BindHost, "[]"))
	return func(c *gin.Context) {
		host := c.Request.Host
		switch {
		case len(pinned) > 0:
			ok := false
			for _, p := range pinned {
				if strings.EqualFold(host, p) {
					ok = true
					break
				}
			}
			if !ok {
				abortError(c, http.StatusForbidden, "Host 不被允许")
				return
			}
		case loopback:
			name := host
			if h, _, err := net.SplitHostPort(host); err == nil {
				name = h
			}
			name = strings.ToLower(strings.Trim(name, "[]"))
			if !isLoopbackHost(name) && !strings.HasSuffix(name, ".localhost") {
				abortError(c, http.StatusForbidden, "Host 不被允许")
				return
			}
		}
		c.Next()
	}
}

// fetchMetadataOK rejects requests a browser marks as coming from another
// origin. Requests authenticated with an Authorization header are exempt: a
// cross-origin page cannot attach one without a CORS preflight, which this
// server never answers. Same-site is rejected too — another localhost port
// is same-site and would otherwise receive our SameSite=Strict cookie.
func fetchMetadataOK(r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		return true
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false
	}
	if d := r.Header.Get("Sec-Fetch-Dest"); d == "iframe" || d == "frame" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if !strings.EqualFold(origin, scheme+"://"+r.Host) {
			return false
		}
	}
	return true
}

var publicAPIPaths = map[string]bool{
	"/api/v1/auth/login": true,
	BootstrapPath:        true,
}

func (s *Server) apiGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !fetchMetadataOK(c.Request) {
			abortError(c, http.StatusForbidden, "拒绝跨站请求")
			return
		}
		if publicAPIPaths[c.Request.URL.Path] {
			c.Next()
			return
		}
		tok, _ := requestToken(c.Request)
		if !s.tokenMatches(tok) {
			abortError(c, http.StatusUnauthorized, "未登录或令牌无效")
			return
		}
		c.Next()
	}
}

func (s *Server) setSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     SessionCookie,
		Value:    s.opts.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

type loginRequest struct {
	Token string `json:"token" binding:"required"`
}

func (s *Server) login(c *gin.Context) {
	if !s.limiter.allow("login") {
		abortError(c, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
		return
	}
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "请提供访问令牌")
		return
	}
	if !s.tokenMatches(strings.TrimSpace(req.Token)) {
		slog.Warn("auth: login with invalid token", "remote", c.ClientIP())
		abortError(c, http.StatusUnauthorized, "令牌无效")
		return
	}
	s.setSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"authenticated": true})
}

func (s *Server) logout(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	c.JSON(http.StatusOK, gin.H{"authenticated": false})
}

func (s *Server) me(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"authenticated": true})
}

// The desktop window opens this as its start URL; in server mode it doubles
// as a one-click login link.
func (s *Server) bootstrap(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	if !s.tokenMatches(c.Query(tokenParam)) {
		abortError(c, http.StatusForbidden, "令牌无效")
		return
	}
	s.setSessionCookie(c)
	c.Redirect(http.StatusSeeOther, "/")
}

func (s *Server) BootstrapURL(origin string) string {
	return origin + BootstrapPath + "?" + tokenParam + "=" + s.opts.Token
}

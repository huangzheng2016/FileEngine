// Package auth is the one access-token check shared by the login routes and
// the auth middleware. There are no users: one token per instance, accepted as
// `Authorization: Bearer <token>` or as the session cookie it is exchanged for.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"strings"

	"FileEngine/biz/errs"
)

const (
	SessionCookie = "fe_session"
	BootstrapPath = "/api/auth/bootstrap"
	TokenParam    = "token"
)

type Options struct {
	Token string
	// BindHost is the listen host. A loopback host enables the DNS-rebinding
	// guard: only localhost / 127.0.0.1 / ::1 Host headers are served.
	BindHost string
}

type Service struct {
	token    string
	loopback bool
}

func NewService(opts Options) (*Service, error) {
	if opts.Token == "" {
		return nil, errors.New("auth: empty access token")
	}
	return &Service{token: opts.Token, loopback: isLoopbackHost(strings.Trim(opts.BindHost, "[]"))}, nil
}

// NewToken returns a fresh random access token.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Service) TokenMatches(candidate string) bool {
	return candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(s.token)) == 1
}

// Token is the value of the session cookie.
func (s *Service) Token() string { return s.token }

// Login checks a token submitted by the login form.
func (s *Service) Login(candidate, remote string) error {
	if !s.TokenMatches(strings.TrimSpace(candidate)) {
		slog.Warn("auth: login with invalid token", "remote", remote)
		return errs.TokenInvalid
	}
	return nil
}

// Bootstrap checks the token of the one-click login link.
func (s *Service) Bootstrap(candidate string) error {
	if !s.TokenMatches(candidate) {
		return errs.TokenInvalid
	}
	return nil
}

// BootstrapURL is the one-click login link for origin.
func (s *Service) BootstrapURL(origin string) string {
	return origin + BootstrapPath + "?" + TokenParam + "=" + s.token
}

// HostAllowed rejects Host headers that are not ours when bound to loopback
// (DNS rebinding: a page on evil.example resolving to 127.0.0.1 would otherwise
// talk to us same-origin). A public bind serves any Host and relies on the token.
func (s *Service) HostAllowed(host string) bool {
	if !s.loopback {
		return true
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.ToLower(strings.Trim(name, "[]"))
	return isLoopbackHost(name) || strings.HasSuffix(name, ".localhost")
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// The desktop UI is served by the in-process Gin server on a random
// 127.0.0.1 port instead of through the Wails asset handler, because the
// Wails v3 beta.27 asset server cannot stream on Windows: its WebView2
// response writer buffers the whole body and hands it over only when the
// handler returns (internal/assetserver/webview/responsewriter_windows.go
// deliberately does not implement http.Flusher), so the never-ending SSE
// event stream would never reach the page. WebKitGTK (pipe) and WKWebView
// (didReceiveData) do stream, but one transport for all three platforms
// wins over a per-OS split.
//
// Any local process can connect to a loopback port, so every request must
// prove it comes from our window: the window opens the one-time bootstrap URL
// carrying a per-launch secret, which sets an HttpOnly SameSite=Strict
// cookie; every other request must present that cookie. Host and Origin are
// pinned to the exact listener address (DNS rebinding, cross-origin pages)
// and responses forbid framing.
const (
	desktopSessionPath   = "/__desktop/session"
	desktopSessionCookie = "fe_desktop_session"
	desktopTokenParam    = "token"
)

// newSessionToken returns a 256-bit URL-safe random secret.
func newSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// listenLoopback binds a random free port on the IPv4 loopback interface.
func listenLoopback() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

type loopbackGuard struct {
	host   string // exact Host header: 127.0.0.1:<port>
	origin string // exact Origin: http://127.0.0.1:<port>
	token  string
	next   http.Handler
}

func newLoopbackGuard(addr net.Addr, token string, next http.Handler) *loopbackGuard {
	host := addr.String()
	return &loopbackGuard{host: host, origin: "http://" + host, token: token, next: next}
}

// bootstrapURL is the window start URL that exchanges the secret for the
// session cookie and redirects to the SPA.
func (g *loopbackGuard) bootstrapURL() string {
	return g.origin + desktopSessionPath + "?" + url.Values{desktopTokenParam: {g.token}}.Encode()
}

func (g *loopbackGuard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "frame-ancestors 'none'")

	if !strings.EqualFold(r.Host, g.host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest == "iframe" || dest == "frame" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != g.origin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !fetchSiteTrusted(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if r.URL.Path == desktopSessionPath {
		g.bootstrap(w, r)
		return
	}
	cookie, err := r.Cookie(desktopSessionCookie)
	if err != nil || !g.tokenMatches(cookie.Value) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	g.next.ServeHTTP(w, r)
}

func (g *loopbackGuard) bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !g.tokenMatches(r.URL.Query().Get(desktopTokenParam)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     desktopSessionCookie,
		Value:    g.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (g *loopbackGuard) tokenMatches(candidate string) bool {
	return candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(g.token)) == 1
}

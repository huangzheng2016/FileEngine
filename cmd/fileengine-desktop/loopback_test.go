package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func newTestGuard(t *testing.T) (*loopbackGuard, *bool) {
	t.Helper()
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 43210}
	return newLoopbackGuard(addr, "s3cret-token", next), &reached
}

func serve(g http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	return rec
}

func TestLoopbackGuardBootstrapSetsCookie(t *testing.T) {
	g, _ := newTestGuard(t)
	u, err := url.Parse(g.bootstrapURL())
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "127.0.0.1:43210" || u.Path != desktopSessionPath {
		t.Fatalf("unexpected bootstrap URL %s", u)
	}
	rec := serve(g, httptest.NewRequest(http.MethodGet, u.String(), nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("bootstrap = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != desktopSessionCookie || cookies[0].Value != "s3cret-token" ||
		!cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected cookie %+v", cookies)
	}
}

func TestLoopbackGuardRejectsBadBootstrap(t *testing.T) {
	g, _ := newTestGuard(t)
	for _, target := range []string{
		"http://127.0.0.1:43210" + desktopSessionPath,
		"http://127.0.0.1:43210" + desktopSessionPath + "?token=wrong",
	} {
		if rec := serve(g, httptest.NewRequest(http.MethodGet, target, nil)); rec.Code != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", target, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, g.bootstrapURL(), nil)
	if rec := serve(g, req); rec.Code != http.StatusForbidden {
		t.Errorf("POST bootstrap = %d, want 403", rec.Code)
	}
}

func authed(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.AddCookie(&http.Cookie{Name: desktopSessionCookie, Value: "s3cret-token"})
	return req
}

func TestLoopbackGuardRequiresCookie(t *testing.T) {
	g, reached := newTestGuard(t)
	if rec := serve(g, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:43210/api/v1/sessions", nil)); rec.Code != http.StatusForbidden || *reached {
		t.Fatalf("no cookie = %d reached=%v", rec.Code, *reached)
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:43210/api/v1/sessions", nil)
	req.AddCookie(&http.Cookie{Name: desktopSessionCookie, Value: "nope"})
	if rec := serve(g, req); rec.Code != http.StatusForbidden || *reached {
		t.Fatalf("wrong cookie = %d reached=%v", rec.Code, *reached)
	}
	rec := serve(g, authed(http.MethodGet, "http://127.0.0.1:43210/api/v1/sessions"))
	if rec.Code != http.StatusNoContent || !*reached {
		t.Fatalf("valid cookie = %d reached=%v", rec.Code, *reached)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("anti-framing header missing")
	}
}

func TestLoopbackGuardPinsHostOriginAndFetchMetadata(t *testing.T) {
	g, reached := newTestGuard(t)
	cases := map[string]func(*http.Request){
		"rebound host":   func(r *http.Request) { r.Host = "evil.example:43210" },
		"localhost host": func(r *http.Request) { r.Host = "localhost:43210" },
		"other origin":   func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:9999") },
		"null origin":    func(r *http.Request) { r.Header.Set("Origin", "null") },
		"cross-site":     func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"iframe":         func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "iframe") },
	}
	for name, mutate := range cases {
		req := authed(http.MethodPost, "http://127.0.0.1:43210/api/v1/sessions")
		mutate(req)
		if rec := serve(g, req); rec.Code != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", name, rec.Code)
		}
	}
	if *reached {
		t.Fatal("rejected request reached the app")
	}
	req := authed(http.MethodPost, "http://127.0.0.1:43210/api/v1/sessions")
	req.Header.Set("Origin", "http://127.0.0.1:43210")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if rec := serve(g, req); rec.Code != http.StatusNoContent {
		t.Fatalf("same-origin POST = %d", rec.Code)
	}
}

// TestLoopbackStreamsIncrementally pins the reason for the loopback design:
// an SSE event written and flushed reaches the client before the handler
// returns.
func TestLoopbackStreamsIncrementally(t *testing.T) {
	release := make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: ping\ndata: {}\n\n"))
		w.(http.Flusher).Flush()
		<-release
	})
	ln, err := listenLoopback()
	if err != nil {
		t.Fatal(err)
	}
	g := newLoopbackGuard(ln.Addr(), "tok", next)
	srv := &http.Server{Handler: g}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	defer close(release)

	req, _ := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+"/api/v1/sessions/1/events/stream", nil)
	req.AddCookie(&http.Cookie{Name: desktopSessionCookie, Value: "tok"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	n, err := resp.Body.Read(buf)
	if err != nil || string(buf[:n]) != "event: ping\ndata: {}\n\n" {
		t.Fatalf("first chunk %q err=%v", buf[:n], err)
	}
}

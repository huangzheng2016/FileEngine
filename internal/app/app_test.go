package app

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"FileEngine/biz/handler/api"
	"FileEngine/biz/mw"
	"FileEngine/container"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	mw.ResetRateLimits()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "database:\n  driver: sqlite\n  dsn: " + filepath.ToSlash(filepath.Join(dir, "fe.db")) + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	frontend := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>fe-test</title>")}}
	a, err := New(Options{ConfigPath: cfgPath, Frontend: frontend})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func serve(t *testing.T, a *App) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ln); err != nil {
		t.Fatal(err)
	}
	return "http://" + ln.Addr().String()
}

// no keep-alive: Hertz waits for idle connections at shutdown
var testClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

func token() string { return config.Get().Server.Token }

func do(t *testing.T, client *http.Client, method, url, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func bearer() map[string]string { return map[string]string{"Authorization": "Bearer " + token()} }

func TestAuthDefaultDeny(t *testing.T) {
	a := newTestApp(t)
	base := serve(t, a)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	c := testClient

	if st, _ := do(t, c, "GET", base+"/healthz", "", nil); st != 200 {
		t.Fatalf("healthz = %d", st)
	}
	for _, path := range []string{"/api/auth/me", "/api/session/list", "/api/file/list"} {
		st, body := do(t, c, "GET", base+path, "", nil)
		if st != 401 || body["code"] != float64(1002) || body["msg"] == "" {
			t.Fatalf("%s without token = %d %v", path, st, body)
		}
	}
	if st, body := do(t, c, "GET", base+"/api/auth/me", "", bearer()); st != 200 || body["authenticated"] != true {
		t.Fatalf("me with token = %d %v", st, body)
	}
	if st, _ := do(t, c, "GET", base+"/api/auth/me", "", map[string]string{"Authorization": "Bearer nope"}); st != 401 {
		t.Fatalf("me with bad token = %d", st)
	}
	// A generated handler no lane has filled answers the enveloped 501.
	if st, body := do(t, c, "GET", base+"/api/session/list", "", bearer()); st != 501 || body["code"] != float64(1009) {
		t.Fatalf("stub = %d %v", st, body)
	}
	if st, body := do(t, c, "GET", base+"/api/nope", "", bearer()); st != 404 || body["code"] != float64(1004) {
		t.Fatalf("unknown api route = %d %v", st, body)
	}
}

func TestLoginCookieAndBootstrap(t *testing.T) {
	a := newTestApp(t)
	base := serve(t, a)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if st, _ := do(t, c, "POST", base+"/api/auth/login", `{"token":"wrong"}`, map[string]string{"Content-Type": "application/json"}); st != 401 {
		t.Fatalf("login wrong = %d", st)
	}
	if st, body := do(t, c, "POST", base+"/api/auth/login", `{"token":"`+token()+`"}`, map[string]string{"Content-Type": "application/json"}); st != 200 || body["authenticated"] != true {
		t.Fatalf("login = %d %v", st, body)
	}
	if st, _ := do(t, c, "GET", base+"/api/auth/me", "", nil); st != 200 {
		t.Fatalf("me with cookie = %d", st)
	}
	if st, _ := do(t, c, "POST", base+"/api/auth/logout", "{}", map[string]string{"Content-Type": "application/json"}); st != 200 {
		t.Fatalf("logout = %d", st)
	}
	if st, _ := do(t, c, "GET", base+"/api/auth/me", "", nil); st != 401 {
		t.Fatalf("me after logout = %d", st)
	}

	jar2, _ := cookiejar.New(nil)
	c2 := &http.Client{Jar: jar2, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c2.Get(base + "/api/auth/bootstrap?token=" + token())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("bootstrap = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if st, _ := do(t, c2, "GET", base+"/api/auth/me", "", nil); st != 200 {
		t.Fatalf("me after bootstrap = %d", st)
	}
	if st, _ := do(t, testClient, "GET", base+"/api/auth/bootstrap?token=bad", "", nil); st != 401 {
		t.Fatalf("bootstrap bad token = %d", st)
	}
}

func TestLoginAndBootstrapShareRateLimit(t *testing.T) {
	a := newTestApp(t)
	base := serve(t, a)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	// The budget is process-wide and earlier tests may have used some of it, so
	// count how many alternating attempts pass before the first 429: sharing
	// means at most the 10 of one budget, not 10 per route.
	passed := 0
	for i := 0; i < 24; i++ {
		var st int
		if i%2 == 0 {
			st, _ = do(t, testClient, "POST", base+"/api/auth/login", `{"token":"x"}`, map[string]string{"Content-Type": "application/json"})
		} else {
			st, _ = do(t, testClient, "GET", base+"/api/auth/bootstrap?token=x", "", nil)
		}
		if st == 429 {
			if passed > 10 {
				t.Fatalf("%d attempts passed before 429; login and bootstrap must share one budget of 10", passed)
			}
			return
		}
		if st != 401 {
			t.Fatalf("attempt %d = %d", i, st)
		}
		passed++
	}
	t.Fatal("no 429 after 24 attempts")
}

func TestRequestGuards(t *testing.T) {
	a := newTestApp(t)
	base := serve(t, a)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	c := testClient

	if st, body := do(t, c, "GET", base+"/healthz", "", map[string]string{"Host": "evil.example"}); st != 403 || body["code"] != float64(1003) {
		t.Fatalf("foreign Host = %d %v", st, body)
	}
	cross := map[string]string{"Sec-Fetch-Site": "cross-site"}
	if st, _ := do(t, c, "GET", base+"/api/auth/me", "", cross); st != 403 {
		t.Fatalf("cross-site cookie request = %d", st)
	}
	cross["Authorization"] = "Bearer " + token()
	if st, _ := do(t, c, "GET", base+"/api/auth/me", "", cross); st != 200 {
		t.Fatalf("cross-site with bearer = %d", st)
	}
	big := `{"token":"` + strings.Repeat("a", 2<<20) + `"}`
	if st, body := do(t, c, "POST", base+"/api/auth/login", big, map[string]string{"Content-Type": "application/json"}); st != 413 || body["code"] != float64(1006) {
		t.Fatalf("2MB body = %d %v", st, body)
	}
	req, _ := http.NewRequest("GET", base+"/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Frame-Options") != "DENY" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("security headers missing: %v", resp.Header)
	}
	// deep links fall back to the SPA
	resp, err = c.Get(base + "/sessions/1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("SPA fallback = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func newSession(t *testing.T) uint {
	t.Helper()
	s := &db.ScanSession{Status: "scanned"}
	if err := container.Default.Repo.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func streamURL(base string, sid uint, extra string) string {
	return base + "/api/event/stream?session_id=" + strconv.FormatUint(uint64(sid), 10) + extra
}

// readFrame returns the next non-comment SSE frame.
func readFrame(t *testing.T, r *bufio.Reader) (id, event, data string) {
	t.Helper()
	for {
		var lines []string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("read frame: %v", err)
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			lines = append(lines, line)
		}
		for _, l := range lines {
			switch {
			case strings.HasPrefix(l, "id:"):
				id = strings.TrimPrefix(l[3:], " ")
			case strings.HasPrefix(l, "event:"):
				event = strings.TrimPrefix(l[6:], " ")
			case strings.HasPrefix(l, "data:"):
				data = strings.TrimPrefix(l[5:], " ")
			}
		}
		if event != "" {
			return
		}
	}
}

func TestEventStreamReplayLiveResumeAndHeartbeat(t *testing.T) {
	prev := api.SSEHeartbeat
	api.SSEHeartbeat = 50 * time.Millisecond
	t.Cleanup(func() { api.SSEHeartbeat = prev })

	a := newTestApp(t)
	base := serve(t, a)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	sid := newSession(t)
	run, err := container.Default.Repo.CreateRun(sid, db.RunPhaseTagging, "t")
	if err != nil {
		t.Fatal(err)
	}
	db.Emit(sid, run.ID, db.EvProgress, db.ProgressData{RunID: run.ID, Phase: "tagging", Total: 1})
	db.Emit(sid, run.ID, db.EvSessionUpdated, struct{}{})

	open := func(url, lastID string) (*http.Response, *bufio.Reader) {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+token())
		if lastID != "" {
			req.Header.Set("Last-Event-ID", lastID)
		}
		resp, err := testClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatalf("stream = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		return resp, bufio.NewReader(resp.Body)
	}

	resp, r := open(streamURL(base, sid, ""), "")
	if _, ev, _ := readFrame(t, r); ev != db.EvRunStatus {
		t.Fatalf("first replayed event = %q", ev)
	}
	id1, ev1, _ := readFrame(t, r)
	if ev1 != db.EvProgress {
		t.Fatalf("second replayed event = %q", ev1)
	}
	_, ev2, _ := readFrame(t, r)
	if ev2 != db.EvSessionUpdated {
		t.Fatalf("second replayed event = %q", ev2)
	}
	db.Emit(sid, run.ID, db.EvPlanUpdated, db.PlanUpdatedData{Version: 1, Status: "draft"})
	_, ev3, _ := readFrame(t, r)
	if ev3 != db.EvPlanUpdated {
		t.Fatalf("live event = %q", ev3)
	}
	resp.Body.Close()

	// resume after the first event: the other two replay, in order
	resp, r = open(streamURL(base, sid, ""), id1)
	defer resp.Body.Close()
	if _, ev, _ := readFrame(t, r); ev != db.EvSessionUpdated {
		t.Fatalf("resumed first = %q", ev)
	}
	if _, ev, _ := readFrame(t, r); ev != db.EvPlanUpdated {
		t.Fatalf("resumed second = %q", ev)
	}
	// a comment frame arrives while idle
	deadline := time.After(2 * time.Second)
	got := make(chan string, 1)
	go func() {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, ": ping") {
				got <- line
				return
			}
		}
	}()
	select {
	case <-got:
	case <-deadline:
		t.Fatal("no heartbeat comment within 2s")
	}
}

func TestEventStreamUnknownSession(t *testing.T) {
	a := newTestApp(t)
	base := serve(t, a)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	req, _ := http.NewRequest("GET", streamURL(base, 9999, ""), nil)
	req.Header.Set("Authorization", "Bearer "+token())
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("unknown session = %d", resp.StatusCode)
	}
}

func TestShutdownEndsStreams(t *testing.T) {
	a := newTestApp(t)
	base := serve(t, a)
	sid := newSession(t)

	req, _ := http.NewRequest("GET", streamURL(base, sid, ""), nil)
	req.Header.Set("Authorization", "Bearer "+token())
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	if line, err := r.ReadString('\n'); err != nil || !strings.HasPrefix(line, ": stream") {
		t.Fatalf("first line = %q, %v", line, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Shutdown waited %v for an open stream", elapsed)
	}
}

func TestHeartbeatNoticesAClosedClient(t *testing.T) {
	prev := api.SSEHeartbeat
	api.SSEHeartbeat = 20 * time.Millisecond
	t.Cleanup(func() { api.SSEHeartbeat = prev })

	a := newTestApp(t)
	base := serve(t, a)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	sid := newSession(t)

	req, _ := http.NewRequest("GET", streamURL(base, sid, ""), nil)
	req.Header.Set("Authorization", "Bearer "+token())
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil || !strings.HasPrefix(line, ": stream") {
		t.Fatalf("first line = %q, %v", line, err)
	}
	if n := container.Default.Streams.Open(); n != 1 {
		t.Fatalf("open streams = %d, want 1", n)
	}
	// The client vanishes without any shutdown: only a failed heartbeat write can
	// tell the handler (Hertz does not sense disconnects on Windows).
	resp.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for container.Default.Streams.Open() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("handler still registered 3s after the client closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	backdate = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	errTest  = errors.New("boom")
)

func TestFindFreePort(t *testing.T) {
	port, err := findFreePort()
	if err != nil {
		t.Fatalf("findFreePort: %v", err)
	}
	if port < backendMinPort || port >= backendMaxPort {
		t.Fatalf("port %d outside [%d, %d)", port, backendMinPort, backendMaxPort)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		t.Fatalf("returned port %d not listenable: %v", port, err)
	}
	_ = ln.Close()
}

func TestEnsureDefaultConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	ensureDefaultConfig(path, 12345)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), "port: 12345") {
		t.Fatalf("config missing port, got:\n%s", data)
	}

	// Existing config must never be overwritten.
	ensureDefaultConfig(path, 9999)
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "port: 12345") {
		t.Fatalf("existing config was overwritten:\n%s", data)
	}
}

func TestEnsureBackendBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "fileengine")

	if err := ensureBackendBinary(nil, target); err == nil {
		t.Fatal("expected error for empty embedded binary")
	}

	v1 := []byte("fake-backend-v1")
	if err := ensureBackendBinary(v1, target); err != nil {
		t.Fatalf("first install: %v", err)
	}
	st, err := os.Stat(target)
	if err != nil {
		t.Fatalf("installed binary missing: %v", err)
	}
	if st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("installed binary not executable: %v", st.Mode())
	}

	// Second install with identical content must skip the rewrite.
	_ = os.Chtimes(target, backdate, backdate)
	if err := ensureBackendBinary(v1, target); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if st, _ := os.Stat(target); !st.ModTime().Equal(backdate) {
		t.Fatal("identical binary was rewritten, want skip")
	}

	// Changed content must be installed.
	v2 := []byte("fake-backend-v2")
	if err := ensureBackendBinary(v2, target); err != nil {
		t.Fatalf("upgrade install: %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != string(v2) {
		t.Fatalf("binary not upgraded, got %q", data)
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("stale .tmp file left behind")
	}
}

func TestServeStartingLoadingPage(t *testing.T) {
	app := &DesktopApp{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	app.handleRequest(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("X-Backend-State"); got != "starting" {
		t.Fatalf("X-Backend-State = %q, want starting", got)
	}
	if !strings.Contains(rec.Body.String(), "后端启动中") {
		t.Fatalf("loading page body unexpected: %s", rec.Body.String())
	}
}

func TestServeStartingErrorPage(t *testing.T) {
	app := &DesktopApp{backendErr: errTest}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")

	app.handleRequest(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("X-Backend-State"); got != "error" {
		t.Fatalf("X-Backend-State = %q, want error", got)
	}
	if !strings.Contains(rec.Body.String(), "后端启动失败") || !strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("error page body unexpected: %s", rec.Body.String())
	}
}

func TestServeStartingNonHTML(t *testing.T) {
	app := &DesktopApp{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.Header.Set("Accept", "application/json")

	app.handleRequest(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "text/html") {
		t.Fatalf("non-HTML request got HTML response: %q", ct)
	}
}

func TestHandleRequestProxies(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "proxied:%s", r.URL.Path)
	}))
	defer backend.Close()

	target, _ := url.Parse(backend.URL)
	app := &DesktopApp{proxy: httputil.NewSingleHostReverseProxy(target)}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	app.handleRequest(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "proxied:/api/ping" {
		t.Fatalf("proxy response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandleRequestSSE(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("backend saw Accept %q, want text/event-stream", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: hello\n\n")
	}))
	defer backend.Close()

	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	app := &DesktopApp{backendPort: port, proxy: httputil.NewSingleHostReverseProxy(mustURL(backend.URL))}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/1/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	app.handleRequest(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if rec.Body.String() != "data: hello\n\n" {
		t.Fatalf("SSE body = %q", rec.Body.String())
	}
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func TestResolveBackendPort(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Missing config: created with a fresh in-range port.
	port, err := resolveBackendPort(configPath)
	if err != nil {
		t.Fatalf("fresh config: %v", err)
	}
	if port < backendMinPort || port >= backendMaxPort {
		t.Fatalf("fresh port %d out of range", port)
	}
	data, _ := os.ReadFile(configPath)
	if !strings.Contains(string(data), fmt.Sprintf("port: %d", port)) {
		t.Fatalf("config missing chosen port:\n%s", data)
	}

	// Existing config with a free port: reused, file untouched.
	port2, err := resolveBackendPort(configPath)
	if err != nil || port2 != port {
		t.Fatalf("existing config: port %d→%d, err %v", port, port2, err)
	}

	// Existing config with an occupied port: port line rewritten, other
	// content preserved.
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	_, busyStr, _ := net.SplitHostPort(ln.Addr().String())
	defer ln.Close()
	custom := "server:\n  port: " + busyStr + "\n  host: 127.0.0.1\nagent:\n  batch_size: 42\n"
	if err := os.WriteFile(configPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	port3, err := resolveBackendPort(configPath)
	if err != nil {
		t.Fatalf("busy port: %v", err)
	}
	if fmt.Sprint(port3) == busyStr {
		t.Fatal("busy port was not replaced")
	}
	data, _ = os.ReadFile(configPath)
	if !strings.Contains(string(data), "batch_size: 42") || !strings.Contains(string(data), fmt.Sprintf("port: %d", port3)) {
		t.Fatalf("rewrite lost content or port:\n%s", data)
	}

	// Config without a parseable server.port: clear error, no clobber.
	if err := os.WriteFile(configPath, []byte("server: {port: 8080}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveBackendPort(configPath); err == nil {
		t.Fatal("flow-style config should error")
	}
	data, _ = os.ReadFile(configPath)
	if string(data) != "server: {port: 8080}\n" {
		t.Fatal("unparseable config was modified")
	}
}

func TestFindServerPortLine(t *testing.T) {
	lines := strings.Split("# comment\nserver:\n  host: 127.0.0.1\n  port: 45001 # inline\nagent:\n  port: 9\n", "\n")
	idx, port := findServerPortLine(lines)
	if idx != 3 || port != 45001 {
		t.Fatalf("idx=%d port=%d, want 3/45001", idx, port)
	}

	// server block ends at the next top-level key.
	lines = strings.Split("agent:\n  port: 9\nserver:\n  host: x\n", "\n")
	if idx, port := findServerPortLine(lines); idx != -1 || port != 0 {
		t.Fatalf("idx=%d port=%d, want -1/0", idx, port)
	}
}

func TestReplacePortValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  port: 45001", "  port: 45002"},
		{"    port: 45001", "    port: 45002"},
		{"\tport: 45001", "\tport: 45002"},
		{"  port: 45001 # primary", "  port: 45002 # primary"},
		{"    port: 45001    # primary port", "    port: 45002 # primary port"},
	}
	for _, c := range cases {
		if got := replacePortValue(c.in, 45002); got != c.want {
			t.Errorf("replacePortValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveBackendPortPreservesIndentCommentAndMode(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, busyStr, _ := net.SplitHostPort(ln.Addr().String())

	// 4-space indentation, inline comment on the port line, tightened mode.
	custom := "server:\n    port: " + busyStr + " # primary port\n    host: 127.0.0.1\nagent:\n    batch_size: 42\n"
	if err := os.WriteFile(configPath, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}

	port, err := resolveBackendPort(configPath)
	if err != nil {
		t.Fatalf("busy port: %v", err)
	}
	if fmt.Sprint(port) == busyStr {
		t.Fatal("busy port was not replaced")
	}

	data, _ := os.ReadFile(configPath)
	want := "    port: " + fmt.Sprint(port) + " # primary port"
	if !strings.Contains(string(data), want) {
		t.Fatalf("indent/comment not preserved, want line %q in:\n%s", want, data)
	}

	// The rewritten file must still parse as YAML with consistent indent.
	var parsed struct {
		Server struct {
			Port int `yaml:"port"`
		} `yaml:"server"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("rewritten config no longer parses: %v\n%s", err, data)
	}
	if parsed.Server.Port != port {
		t.Fatalf("parsed port = %d, want %d", parsed.Server.Port, port)
	}

	st, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", st.Mode().Perm())
	}
}

func TestStopBackendKillsChildAndIsIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sleep and SIGTERM")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	done := make(chan struct{})
	app := &DesktopApp{backendCmd: cmd, backendDone: done}
	app.watchBackend(cmd, done, nil)

	app.stopBackend()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("backend child not stopped within 5s")
	}

	// Second call (as issued after wails.Run returns) must not block or panic.
	finished := make(chan struct{})
	go func() {
		app.stopBackend()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("second stopBackend call blocked")
	}
}

func TestStopBackendNoBackend(t *testing.T) {
	app := &DesktopApp{}
	app.stopBackend() // must be a no-op, not a panic
	app.stopBackend()
}

func TestLoadWindowState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	statePath := filepath.Join(home, ".fileengine", "gui_state.json")

	// Missing file → defaults.
	if s := loadWindowState(); s.Width != defaultWindowWidth || s.Height != defaultWindowHeight {
		t.Fatalf("missing file: got %+v, want defaults", s)
	}

	// Valid file → restored.
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"width":1600,"height":1000}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := loadWindowState(); s.Width != 1600 || s.Height != 1000 {
		t.Fatalf("valid file: got %+v", s)
	}

	// Out-of-range → defaults.
	_ = os.WriteFile(statePath, []byte(`{"width":10,"height":10}`), 0o644)
	if s := loadWindowState(); s.Width != defaultWindowWidth {
		t.Fatalf("out-of-range: got %+v, want defaults", s)
	}

	// Corrupt JSON → defaults.
	_ = os.WriteFile(statePath, []byte(`{`), 0o644)
	if s := loadWindowState(); s.Width != defaultWindowWidth {
		t.Fatalf("corrupt: got %+v, want defaults", s)
	}
}

func TestOpenBackendLogRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backend.log")

	// Fresh create.
	f := openBackendLog(path)
	if f == nil {
		t.Fatal("openBackendLog returned nil")
	}
	_, _ = f.WriteString("line1\n")
	_ = f.Close()

	// Oversized file is rotated to .1 before appending.
	big := make([]byte, backendLogMaxBytes+1)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	f = openBackendLog(path)
	if f == nil {
		t.Fatal("openBackendLog after rotation returned nil")
	}
	_ = f.Close()
	st, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	if st.Size() != int64(backendLogMaxBytes+1) {
		t.Fatalf("rotated size = %d", st.Size())
	}
	if st, _ := os.Stat(path); st.Size() != 0 {
		t.Fatalf("new log not empty: %d", st.Size())
	}
}

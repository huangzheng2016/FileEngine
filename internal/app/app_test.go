package app

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"FileEngine/internal/config"
	"FileEngine/internal/secret"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "database:\n  driver: sqlite\n  dsn: " + filepath.ToSlash(filepath.Join(dir, "fe.db")) + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestShutdownEndsStreams(t *testing.T) {
	a := newTestApp(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	streaming := make(chan struct{})
	handler := http.NewServeMux()
	handler.Handle("/api/", a.Handler())
	handler.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": open\n\n"))
		w.(http.Flusher).Flush()
		close(streaming)
		<-r.Context().Done()
	})
	if err := a.Start(context.Background(), ln, handler); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+"/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+config.Get().Server.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/sessions = %d", resp.StatusCode)
	}

	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/stream")
		if err == nil {
			_, _ = bufio.NewReader(resp.Body).ReadString('\n')
			resp.Body.Close()
		}
	}()
	<-streaming

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Shutdown waited %v for an open stream", elapsed)
	}
	if err := a.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestNewSecuritySetup(t *testing.T) {
	a := newTestApp(t)
	tok := config.Get().Server.Token
	if len(tok) < 40 {
		t.Fatalf("token not generated: %q", tok)
	}
	dir := filepath.Dir(config.Path())
	st, err := os.Stat(filepath.Join(dir, secret.KeyFileName))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("secret key file: %v %v", st, err)
	}
	if a.Config().Server.Host != "127.0.0.1" {
		t.Fatalf("default bind = %q", a.Config().Server.Host)
	}
}

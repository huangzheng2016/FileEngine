//go:build !production

package main

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestStartServerEndToEnd boots the in-process server exactly as the desktop
// does (minus the window) and walks the window's request flow: bootstrap URL
// -> session cookie -> SPA + API; a cookieless client is refused.
func TestStartServerEndToEnd(t *testing.T) {
	webRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("<!doctype html><title>fe-test</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := resolveDesktopPaths(t.TempDir(), envOf(nil), "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.prepare(); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir()) // startServer changes directory; restore afterwards

	service := &Service{}
	guard, err := startServer(service, paths, webRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.ServiceShutdown() })

	if cwd, _ := os.Getwd(); cwd != paths.DataDir {
		t.Fatalf("cwd = %s, want data dir %s", cwd, paths.DataDir)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Get(guard.bootstrapURL())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "fe-test") {
		t.Fatalf("bootstrap -> SPA = %d %q", resp.StatusCode, body)
	}

	resp, err = client.Get(guard.origin + "/api/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("API with cookie = %d", resp.StatusCode)
	}

	resp, err = http.Get(guard.origin + "/api/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("API without cookie = %d, want 403", resp.StatusCode)
	}
}

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goRuntime "runtime"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type DesktopApp struct {
	mu          sync.RWMutex
	backendCmd  *exec.Cmd
	backendPort int
	proxy       *httputil.ReverseProxy
	readyOnce   sync.Once
	readyCh     chan struct{}
	ctx         context.Context
}

func (a *DesktopApp) onStartup(ctx context.Context) {
	a.ctx = ctx
	wailsRuntime.LogInfo(ctx, fmt.Sprintf("FileEngine backend on port %d", a.backendPort))
}

func (a *DesktopApp) onShutdown(_ context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.backendCmd != nil && a.backendCmd.Process != nil {
		_ = a.backendCmd.Process.Kill()
	}
}

func (a *DesktopApp) handleRequest(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	proxy := a.proxy
	a.mu.RUnlock()
	if proxy == nil {
		http.Error(w, "Backend starting...", http.StatusServiceUnavailable)
		return
	}
	// SSE support
	if r.Header.Get("Accept") == "text/event-stream" {
		a.proxySSE(w, r)
		return
	}
	proxy.ServeHTTP(w, r)
}

func (a *DesktopApp) proxySSE(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	port := a.backendPort
	a.mu.RUnlock()

	backendURL := fmt.Sprintf("http://127.0.0.1:%d%s?%s", port, r.URL.Path, r.URL.RawQuery)
	resp, err := http.Get(backendURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
}

func (a *DesktopApp) startBackend() error {
	port, err := findFreePort()
	if err != nil {
		return err
	}

	binPath, err := a.prepareBackend()
	if err != nil {
		return err
	}

	dataDir, err := ensureDataDir()
	if err != nil {
		return err
	}

	configPath := filepath.Join(dataDir, "config.yaml")
	ensureDefaultConfig(configPath, port)

	cmd := exec.Command(binPath, configPath)
	cmd.Dir = dataDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start backend: %w", err)
	}

	if err := waitForBackend(port); err != nil {
		_ = cmd.Process.Kill()
		return err
	}

	targetURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	a.mu.Lock()
	a.backendCmd = cmd
	a.backendPort = port
	a.proxy = proxy
	a.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		if a.ctx != nil {
			wailsRuntime.Quit(a.ctx)
		}
	}()

	a.readyOnce.Do(func() { close(a.readyCh) })
	return nil
}

func (a *DesktopApp) prepareBackend() (string, error) {
	if len(embeddedBackend) == 0 {
		return "", errors.New("no embedded backend binary")
	}
	dataDir, err := ensureDataDir()
	if err != nil {
		return "", err
	}
	ext := ""
	if goRuntime.GOOS == "windows" {
		ext = ".exe"
	}
	target := filepath.Join(dataDir, "fileengine"+ext)
	return target, os.WriteFile(target, embeddedBackend, 0o755)
}

func ensureDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".fileengine")
	return dir, os.MkdirAll(dir, 0o755)
}

func ensureDefaultConfig(path string, port int) {
	if _, err := os.Stat(path); err == nil {
		return // config exists
	}
	cfg := fmt.Sprintf(`server:
  port: %d
  host: 127.0.0.1
database:
  driver: sqlite
  dsn: fileengine.db
agent:
  batch_size: 10
  concurrency: 1
  max_file_read_size: 102400
  max_retries: 3
`, port)
	_ = os.WriteFile(path, []byte(cfg), 0o644)
}

func findFreePort() (int, error) {
	for i := 0; i < 50; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(backendMaxPort-backendMinPort)))
		port := backendMinPort + int(n.Int64())
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return port, nil
	}
	return 0, errors.New("no free port found")
}

func waitForBackend(port int) error {
	deadline := time.Now().Add(backendReadyTimeout)
	addr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(addr)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("backend not ready within %s", backendReadyTimeout)
}

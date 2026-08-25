package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"html"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goRuntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const backendLogMaxBytes = 5 << 20 // 5 MiB

type DesktopApp struct {
	mu           sync.RWMutex
	backendCmd   *exec.Cmd
	backendDone  chan struct{}
	backendPort  int
	backendErr   error
	proxy        *httputil.ReverseProxy
	ctx          context.Context
	shuttingDown bool
}

func (a *DesktopApp) onStartup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	wailsRuntime.LogInfo(ctx, fmt.Sprintf("FileEngine desktop %s", Version))
}

func (a *DesktopApp) appCtx() context.Context {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ctx
}

func (a *DesktopApp) setBackendErr(err error) {
	a.mu.Lock()
	a.backendErr = err
	a.mu.Unlock()
}

func (a *DesktopApp) onShutdown(ctx context.Context) {
	a.saveWindowState(ctx)
	a.mu.Lock()
	a.shuttingDown = true
	a.mu.Unlock()
	a.stopBackend()
}

// stopBackend asks the backend to terminate gracefully, then force-kills it
// after a grace period. It is safe to call multiple times and while the
// backend is still in its health-check window: the exit watcher goroutine
// started in startBackend is the only caller of cmd.Wait and closes
// backendDone, so there is no double Wait and repeated calls return fast.
func (a *DesktopApp) stopBackend() {
	a.mu.RLock()
	cmd := a.backendCmd
	done := a.backendDone
	a.mu.RUnlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = terminateProcess(cmd.Process)
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
}

func (a *DesktopApp) handleRequest(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	proxy := a.proxy
	backendErr := a.backendErr
	a.mu.RUnlock()
	if proxy == nil {
		a.serveStarting(w, r, backendErr)
		return
	}
	// SSE support
	if r.Header.Get("Accept") == "text/event-stream" {
		a.proxySSE(w, r)
		return
	}
	proxy.ServeHTTP(w, r)
}

// serveStarting answers requests that arrive before the backend is wired up.
// HTML navigations get a self-reloading loading page (or a static error page
// once backend startup has failed); anything else gets a plain 503.
func (a *DesktopApp) serveStarting(w http.ResponseWriter, r *http.Request, backendErr error) {
	wantsHTML := r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html")
	if !wantsHTML {
		msg := "backend is starting"
		if backendErr != nil {
			msg = "backend unavailable: " + backendErr.Error()
		}
		http.Error(w, msg, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if backendErr != nil {
		w.Header().Set("X-Backend-State", "error")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, backendErrorPage, html.EscapeString(backendErr.Error()))
		return
	}
	w.Header().Set("X-Backend-State", "starting")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprint(w, backendLoadingPage)
}

func (a *DesktopApp) proxySSE(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	port := a.backendPort
	a.mu.RUnlock()

	backendURL := fmt.Sprintf("http://127.0.0.1:%d%s?%s", port, r.URL.Path, r.URL.RawQuery)
	// Tie the backend request to the client connection: when the client
	// disconnects, r.Context() is canceled and resp.Body.Read unblocks,
	// so the loop exits instead of leaking the backend SSE connection.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, backendURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// Propagate the SSE accept header so the backend sees a faithful request.
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				break // client disconnected
			}
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
	dataDir, err := ensureDataDir()
	if err != nil {
		return err
	}

	configPath := filepath.Join(dataDir, "config.yaml")
	// The backend listens on the port written in config.yaml, so the port
	// must come from the config (not the other way round) — otherwise every
	// launch after the first proxies/polls a port nothing listens on.
	port, err := resolveBackendPort(configPath)
	if err != nil {
		return err
	}

	binPath := filepath.Join(dataDir, backendExecutableName())
	if err := ensureBackendBinary(embeddedBackend, binPath); err != nil {
		return err
	}

	cmd := exec.Command(binPath, configPath)
	cmd.Dir = dataDir
	logPath := filepath.Join(dataDir, "backend.log")
	logFile := openBackendLog(logPath)
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return fmt.Errorf("start backend: %w", err)
	}

	// Register the backend (and its reaper) immediately after Start, before
	// the health check: if the window is closed while waitForBackend is
	// still polling, onShutdown/stopBackend must find the child and kill it
	// instead of leaving an orphan behind.
	done := make(chan struct{})
	a.mu.Lock()
	a.backendCmd = cmd
	a.backendDone = done
	shuttingDown := a.shuttingDown
	a.mu.Unlock()
	a.watchBackend(cmd, done, logFile)

	if shuttingDown {
		// onShutdown already ran before this child existed; kill it now,
		// the watcher reaps it and closes the log file.
		_ = cmd.Process.Kill()
		return errors.New("backend startup aborted: application is shutting down")
	}

	if err := waitForBackend(port); err != nil {
		// The watcher owns cmd.Wait and reaps the killed child.
		_ = cmd.Process.Kill()
		return fmt.Errorf("%w (backend log: %s)", err, logPath)
	}

	targetURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	a.mu.Lock()
	a.backendPort = port
	a.proxy = proxy
	a.mu.Unlock()

	return nil
}

// watchBackend reaps the backend process exactly once: it is the only caller
// of cmd.Wait, closes the log file, and closes done (which stopBackend waits
// on). When a fully started backend (proxy wired up) dies on its own, the
// error is surfaced via backendErr and the app quits; exits before that
// point are reported by startBackend itself, and exits during shutdown are
// expected — neither needs the error page or a Quit call.
func (a *DesktopApp) watchBackend(cmd *exec.Cmd, done chan struct{}, logFile *os.File) {
	go func() {
		waitErr := cmd.Wait()
		if logFile != nil {
			_ = logFile.Close()
		}
		close(done)
		exitErr := fmt.Errorf("backend exited unexpectedly: %v", waitErr)
		if waitErr == nil {
			exitErr = errors.New("backend exited unexpectedly with status 0")
		}
		a.mu.Lock()
		running := a.proxy != nil
		if running {
			a.backendErr = exitErr
			a.proxy = nil
		}
		a.mu.Unlock()
		log.Printf("fileengine backend exited: %v", waitErr)
		if running {
			if ctx := a.appCtx(); ctx != nil {
				wailsRuntime.Quit(ctx)
			}
		}
	}()
}

// ensureBackendBinary installs the embedded backend binary at target,
// skipping the rewrite entirely when the on-disk file is already identical.
// The write is atomic (tmp file + rename) so a crash mid-install can never
// leave a truncated binary behind.
func ensureBackendBinary(data []byte, target string) error {
	if len(data) == 0 {
		return errors.New("no embedded backend binary")
	}
	if current, err := os.ReadFile(target); err == nil && bytes.Equal(current, data) {
		return nil
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func backendExecutableName() string {
	if goRuntime.GOOS == "windows" {
		return "fileengine.exe"
	}
	return "fileengine"
}

// openBackendLog opens the backend log file for appending, rotating it to
// .1 first when it has grown past backendLogMaxBytes. Returns nil (and the
// caller falls back to the process console) when the file cannot be opened.
func openBackendLog(path string) *os.File {
	if st, err := os.Stat(path); err == nil && st.Size() > backendLogMaxBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
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

// resolveBackendPort determines the port the backend will actually listen on.
// The backend reads its listen port from config.yaml, so the config is the
// source of truth: an existing config's server.port is reused when free;
// when it is occupied, only the port line is rewritten (all other settings
// are preserved); a missing config is created with a fresh random port.
func resolveBackendPort(configPath string) (int, error) {
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		port, err := findFreePort()
		if err != nil {
			return 0, err
		}
		ensureDefaultConfig(configPath, port)
		return port, nil
	}
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(data), "\n")
	idx, port := findServerPortLine(lines)
	if idx < 0 || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("cannot determine server.port from %s (block-style YAML expected); please fix the config", configPath)
	}
	if portFree(port) {
		return port, nil
	}

	newPort, err := findFreePort()
	if err != nil {
		return 0, err
	}
	lines[idx] = replacePortValue(lines[idx], newPort)
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return 0, err
	}
	// Keep the original file mode (e.g. 0o600 when the config holds
	// credentials) instead of widening it to the WriteFile default.
	if st, statErr := os.Stat(configPath); statErr == nil {
		_ = os.Chmod(tmp, st.Mode().Perm())
	}
	if err := os.Rename(tmp, configPath); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return newPort, nil
}

// replacePortValue rewrites the port value on a server.port line while
// preserving the original leading whitespace and any trailing inline
// comment, so the rewritten file keeps its indentation style and stays
// parseable.
func replacePortValue(line string, port int) string {
	trimmed := strings.TrimLeft(line, " \t")
	leading := line[:len(line)-len(trimmed)]
	comment := ""
	if i := strings.Index(trimmed, "#"); i >= 0 {
		comment = " " + strings.TrimSpace(trimmed[i:])
	}
	return fmt.Sprintf("%sport: %d%s", leading, port, comment)
}

// findServerPortLine scans block-style YAML lines for the server.port entry
// and returns its line index and value (-1/0 when absent or not numeric).
func findServerPortLine(lines []string) (idx int, port int) {
	inServer := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if line == trimmed {
			// Top-level key: enters or leaves the server block.
			key, _, _ := strings.Cut(trimmed, ":")
			inServer = strings.TrimSpace(key) == "server"
			continue
		}
		if !inServer {
			continue
		}
		field, val, found := strings.Cut(trimmed, ":")
		if found && strings.TrimSpace(field) == "port" {
			val, _, _ = strings.Cut(val, "#")
			p, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return i, 0
			}
			return i, p
		}
	}
	return -1, 0
}

func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
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

const backendLoadingPage = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>FileEngine</title>
<style>
  body { margin:0; min-height:100vh; display:flex; flex-direction:column; align-items:center; justify-content:center; font-family:-apple-system,"PingFang SC","Segoe UI",sans-serif; background:#1e1e2e; color:#cdd6f4; }
  .spinner { width:36px; height:36px; border:3px solid #45475a; border-top-color:#89b4fa; border-radius:50%; animation:spin 0.9s linear infinite; margin-bottom:16px; }
  @keyframes spin { to { transform:rotate(360deg); } }
  p { color:#a6adc8; font-size:14px; }
</style>
</head>
<body>
<div class="spinner"></div>
<p>FileEngine 后端启动中…</p>
<script>
setInterval(function () {
  fetch('/', { cache: 'no-store' }).then(function (r) {
    if (r.status !== 503 || r.headers.get('X-Backend-State') === 'error') location.reload();
  }).catch(function () {});
}, 1000);
</script>
</body>
</html>
`

const backendErrorPage = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>FileEngine</title>
<style>
  body { margin:0; min-height:100vh; display:flex; flex-direction:column; align-items:center; justify-content:center; font-family:-apple-system,"PingFang SC","Segoe UI",sans-serif; background:#1e1e2e; color:#cdd6f4; }
  h1 { font-size:18px; color:#f38ba8; margin-bottom:8px; }
  pre { max-width:80vw; overflow:auto; background:#313244; padding:12px 16px; border-radius:8px; font-size:12px; color:#eba0ac; }
  p { color:#a6adc8; font-size:13px; }
</style>
</head>
<body>
<h1>后端启动失败</h1>
<pre>%s</pre>
<p>详细日志见 ~/.fileengine/backend.log，修复后请重新启动应用。</p>
</body>
</html>
`

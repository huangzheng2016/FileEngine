// Command fileengine-desktop is the Wails v3 desktop shell. It runs the full
// FileEngine server in-process (no subprocess) on a random 127.0.0.1 port
// guarded by a per-launch token, and points its window at that origin; see
// loopback.go for why the Wails asset handler is not used to serve the UI.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"runtime"

	"FileEngine/internal/api"
	"FileEngine/internal/app"
	"FileEngine/internal/logging"

	"github.com/gin-gonic/gin"
	"github.com/wailsapp/wails/v3/pkg/application"
	wailsevents "github.com/wailsapp/wails/v3/pkg/events"
)

// desktopAppID is the product identifier. On Linux it is also the GTK
// application id (Wayland app_id / X11 WM_CLASS); it keys the single-instance
// lock everywhere.
const desktopAppID = "com.fileengine.desktop"

// Version is injected at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

const (
	defaultWindowWidth  = 1400
	defaultWindowHeight = 900
	minWindowWidth      = 1024
	minWindowHeight     = 640
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("fileengine-desktop", flag.ContinueOnError)
	dataDir := flags.String("data-dir", "", "data directory for config.yaml and the SQLite database (default: per-user app data; env "+dataDirEnv+")")
	webRoot := flags.String("web-root", "", "built frontend directory for development builds (default: web/dist; ignored in production builds)")
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Println(Version)
		return 0
	}

	paths, startErr := resolveDesktopPaths(*dataDir, os.Getenv, runtime.GOOS)
	if startErr == nil {
		startErr = paths.prepare()
	}
	logger, closeLog := setupLogging(paths, startErr == nil)
	defer closeLog()
	logger.Info("FileEngine desktop starting", "version", Version, "data_dir", paths.DataDir)

	var window *application.WebviewWindow
	service := &Service{}
	wailsApp := application.New(application.Options{
		Name:        "FileEngine",
		Description: "FileEngine desktop",
		Logger:      logger,
		ErrorHandler: func(err error) {
			logger.Error("Wails error", "error", err)
		},
		Services: []application.Service{application.NewService(service)},
		// The UI is served from the loopback server; the Wails origin only
		// hosts the runtime endpoints, which the guard locks to the app.
		Assets: application.AssetOptions{
			Handler:    http.HandlerFunc(wailsOriginFallback),
			Middleware: runtimeRequestGuard,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{ApplicationID: desktopAppID},
		// application.New exits a second instance right here, before it can
		// touch the database; the first instance comes to the front.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: desktopAppID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if window != nil {
					showWindow(window)
				}
			},
		},
	})

	var startURL string
	if startErr == nil {
		startURL, startErr = startServer(service, paths, *webRoot)
	}
	if startErr != nil {
		return showStartupError(wailsApp, paths, startErr, logger)
	}

	windowOptions := application.WebviewWindowOptions{
		Title:          "FileEngine",
		URL:            startURL,
		Width:          defaultWindowWidth,
		Height:         defaultWindowHeight,
		MinWidth:       minWindowWidth,
		MinHeight:      minWindowHeight,
		EnableFileDrop: true,
	}
	savedWindow, hasSavedWindow := loadWindowState(paths.DataDir)
	if hasSavedWindow {
		windowOptions = applyWindowState(windowOptions, savedWindow, screenBounds(wailsApp))
	}
	window = wailsApp.Window.NewWithOptions(windowOptions)
	trackWindowState(window, paths.DataDir, savedWindow, func(err error) {
		logger.Warn("window state not saved", "error", err)
	})
	window.OnWindowEvent(wailsevents.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		emitFilesDropped(wailsApp, event)
	})
	window.Show()
	if err := wailsApp.Run(); err != nil {
		logger.Error("desktop stopped", "error", err)
		return 1
	}
	return 0
}

// startServer binds the loopback listener and builds the in-process server
// with a per-launch token; it returns the window start URL that logs the
// window in. The Service starts serving when Wails starts.
func startServer(service *Service, paths desktopPaths, webRoot string) (string, error) {
	frontend, err := desktopFrontend(webRoot)
	if err != nil {
		return "", err
	}
	token, err := api.NewToken()
	if err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	ln, err := listenLoopback()
	if err != nil {
		return "", fmt.Errorf("listen on loopback: %w", err)
	}
	var roots []string
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, home)
	}
	a, err := app.New(app.Options{
		ConfigPath:   paths.ConfigFile,
		Frontend:     frontend,
		Token:        token,
		AllowedHosts: []string{ln.Addr().String()},
		LocalRoots:   roots,
	})
	if err != nil {
		ln.Close()
		return "", err
	}
	service.app, service.ln, service.handler = a, ln, a.Handler()
	return a.Server().BootstrapURL("http://" + ln.Addr().String()), nil
}

// setupLogging sends the standard logger, Gin's request log and the Wails
// slog output to the data-dir log file plus stderr. The file comes first in
// the MultiWriter: a GUI build on Windows has no valid stderr, and a failing
// writer stops the ones after it.
func setupLogging(paths desktopPaths, haveDataDir bool) (*slog.Logger, func()) {
	out := io.Writer(os.Stderr)
	closeLog := func() {}
	if haveDataDir {
		if f, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			out = io.MultiWriter(f, os.Stderr)
			closeLog = func() { _ = f.Close() }
		}
	}
	log.SetOutput(out)
	gin.DefaultWriter = out
	gin.DefaultErrorWriter = out
	return logging.Setup(out), closeLog
}

// wailsOriginFallback answers requests to the Wails asset origin, which never
// hosts the UI.
func wailsOriginFallback(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "FileEngine serves its UI from the loopback server", http.StatusNotFound)
}

func showWindow(window *application.WebviewWindow) {
	window.Show()
	if window.IsMinimised() {
		window.UnMinimise()
	}
	window.Focus()
}

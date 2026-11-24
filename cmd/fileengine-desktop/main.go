// Command fileengine-desktop is the Wails v3 desktop shell. The backend runs
// in-process without a listener: the window serves the embedded UI from the
// Wails asset handler and reaches the API through the Service bindings (see
// service.go), which dispatch to the same Hertz routes as the web server.
package main

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"runtime"

	"FileEngine/internal/app"
	"FileEngine/internal/logging"

	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/wailsapp/wails/v3/pkg/application"
	wailsevents "github.com/wailsapp/wails/v3/pkg/events"
)

// desktopAppID is the product identifier. On Linux it is also the GTK
// application id (Wayland app_id / X11 WM_CLASS); it keys the single-instance
// lock everywhere.
const desktopAppID = "com.fileengine.desktop"

//go:embed appicon.png
var appIcon []byte

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
	var assetHandler http.Handler
	wailsApp := application.New(application.Options{
		Name:        "FileEngine",
		Description: "用 AI 整理 NAS 上的文件",
		Icon:        appIcon,
		Logger:      logger,
		ErrorHandler: func(err error) {
			logger.Error("Wails error", "error", err)
		},
		Services: []application.Service{application.NewService(service)},
		// The guard keeps other documents away from the Wails runtime
		// endpoints; everything else is the embedded UI.
		Assets: application.AssetOptions{
			// Set once the backend is up, before the window opens.
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if assetHandler == nil {
					http.NotFound(w, r)
					return
				}
				assetHandler.ServeHTTP(w, r)
			}),
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

	if startErr == nil {
		assetHandler, startErr = startBackend(service, paths, *webRoot)
	}
	if startErr != nil {
		return showStartupError(wailsApp, paths, startErr, logger)
	}

	windowOptions := application.WebviewWindowOptions{
		Title:          "FileEngine",
		URL:            "/",
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

// startBackend opens the database and builds the in-process backend. It
// returns the handler that serves the embedded UI.
func startBackend(service *Service, paths desktopPaths, webRoot string) (http.Handler, error) {
	frontend, err := desktopFrontend(webRoot)
	if err != nil {
		return nil, err
	}
	var roots []string
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, home)
	}
	a, err := app.New(app.Options{
		ConfigPath: paths.ConfigFile,
		Frontend:   frontend,
		Desktop:    true,
		LocalRoots: roots,
	})
	if err != nil {
		return nil, err
	}
	service.app = a
	return app.SPAHandler(frontend), nil
}

// The log file comes first in the MultiWriter: a Windows GUI build has no
// valid stderr, and a failing writer stops the ones after it.
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
	hlog.SetOutput(out)
	return logging.Setup(out), closeLog
}

func showWindow(window *application.WebviewWindow) {
	window.Show()
	if window.IsMinimised() {
		window.UnMinimise()
	}
	window.Focus()
}

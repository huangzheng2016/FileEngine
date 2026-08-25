package main

import (
	_ "embed"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed bin/backend.bin
var embeddedBackend []byte

// Version is injected at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

const (
	backendMinPort      = 45000
	backendMaxPort      = 55000
	backendReadyTimeout = 60 * time.Second
)

func main() {
	log.Printf("FileEngine desktop %s", Version)
	app := &DesktopApp{}

	// GUI-first: open the window immediately and boot the backend in the
	// background. Until the reverse proxy is wired up, handleRequest serves
	// a self-reloading loading page (or an error page if the boot fails),
	// so startup failures are visible in the window instead of the process
	// dying silently before any UI appears.
	go func() {
		if err := app.startBackend(); err != nil {
			app.setBackendErr(err)
			log.Printf("start backend: %v", err)
		}
	}()

	windowState := loadWindowState()

	if err := wails.Run(&options.App{
		Title:     "FileEngine",
		Width:     windowState.Width,
		Height:    windowState.Height,
		MinWidth:  minWindowWidth,
		MinHeight: minWindowHeight,
		AssetServer: &assetserver.Options{
			Handler: http.HandlerFunc(app.handleRequest),
		},
		OnStartup:  app.onStartup,
		OnShutdown: app.onShutdown,
		Debug: options.Debug{
			OpenInspectorOnStartup: os.Getenv("WAILS_ENV") == "dev",
		},
		EnableDefaultContextMenu: true,
	}); err != nil {
		log.Fatalf("Wails error: %v", err)
	}

	// Belt and braces: if the window was closed while the backend was still
	// starting (onShutdown ran before the child was registered, or never
	// ran at all), make sure no orphan process survives main's exit.
	// stopBackend is idempotent and returns fast when already done.
	app.stopBackend()
}

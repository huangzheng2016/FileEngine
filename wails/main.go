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

const (
	backendMinPort      = 45000
	backendMaxPort      = 55000
	backendReadyTimeout = 60 * time.Second
)

func main() {
	app := &DesktopApp{readyCh: make(chan struct{})}

	if err := app.startBackend(); err != nil {
		log.Fatalf("启动后端失败: %v", err)
	}

	select {
	case <-app.readyCh:
	case <-time.After(backendReadyTimeout):
		log.Fatalf("后端启动超时")
	}

	if err := wails.Run(&options.App{
		Title:     "FileEngine",
		Width:     1400,
		Height:    900,
		MinWidth:  1200,
		MinHeight: 720,
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
}

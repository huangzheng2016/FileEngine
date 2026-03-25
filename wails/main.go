package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"errors"
	"fmt"
	"io"
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
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
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

	backendURL := fmt.Sprintf("http://127.0.0.1:%d", app.backendPort)

	if err := wails.Run(&options.App{
		Title:     "FileEngine",
		Width:     1400,
		Height:    900,
		MinWidth:  1200,
		MinHeight: 720,
		AssetServer: &assetserver.Options{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" {
					http.Redirect(w, r, backendURL, http.StatusTemporaryRedirect)
					return
				}
				app.handleRequest(w, r)
			}),
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

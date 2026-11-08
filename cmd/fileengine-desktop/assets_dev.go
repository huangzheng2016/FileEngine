//go:build !production

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const defaultWebRoot = "web/dist"

// Served from disk so a frontend rebuild shows up on reload without
// recompiling Go.
func desktopFrontend(webRoot string) (fs.FS, error) {
	if webRoot == "" {
		webRoot = defaultWebRoot
	}
	abs, err := filepath.Abs(webRoot)
	if err != nil {
		return nil, fmt.Errorf("desktop web assets: resolve %s: %w", webRoot, err)
	}
	info, err := os.Stat(filepath.Join(abs, "index.html"))
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("desktop web assets: index.html not found under %s (run: cd web && npm run build, or pass --web-root)", abs)
	}
	return os.DirFS(abs), nil
}

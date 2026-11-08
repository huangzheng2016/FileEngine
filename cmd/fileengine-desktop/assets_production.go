//go:build production

package main

import (
	"fmt"
	"io/fs"

	"FileEngine/web"
)

// desktopFrontend serves the SPA embedded at build time (web/dist); the
// --web-root flag is ignored in production builds.
func desktopFrontend(string) (fs.FS, error) {
	files, err := web.Dist()
	if err != nil {
		return nil, fmt.Errorf("desktop web assets: open embedded dist: %w", err)
	}
	if info, err := fs.Stat(files, "index.html"); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("desktop web assets: embedded index.html is missing: %w", err)
	}
	return files, nil
}

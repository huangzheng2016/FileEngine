// Package web embeds the built frontend (web/dist) for the server binary and
// the production desktop build. Build the frontend first (cd web && npm run
// build); `task web:stub` writes a placeholder index.html for Go-only checks.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built SPA with index.html at its root.
func Dist() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}

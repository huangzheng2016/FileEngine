package app

import (
	"context"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"FileEngine/biz/errs"
	"FileEngine/biz/handler"
	"FileEngine/biz/handler/resp"
	"FileEngine/biz/router"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

// registerRoutes mounts every /api route from the IDL, the health probe and the
// embedded frontend. Nothing under /api is registered by hand.
func registerRoutes(h *server.Hertz, frontend fs.FS) {
	router.GeneratedRegister(h)
	h.GET("/healthz", handler.Healthz)

	h.NoRoute(func(ctx context.Context, c *app.RequestContext) {
		p := string(c.Path())
		if frontend == nil || p == "/api" || strings.HasPrefix(p, "/api/") {
			resp.Fail(ctx, c, errs.NoSuchRoute)
			return
		}
		serveSPA(c, frontend, p)
	})
}

// serveSPA answers a file of the built frontend, or index.html for any other
// path so deep links such as /sessions/1 reach the router.
func serveSPA(c *app.RequestContext, frontend fs.FS, p string) {
	name := strings.TrimPrefix(path.Clean("/"+p), "/")
	if name != "" {
		if info, err := fs.Stat(frontend, name); err == nil && info.Mode().IsRegular() {
			if data, err := fs.ReadFile(frontend, name); err == nil {
				if strings.HasPrefix(name, "assets/") {
					// Vite content-hashes everything under assets/
					c.Header("Cache-Control", "public, max-age=31536000, immutable")
				}
				c.Data(http.StatusOK, contentType(name), data)
				return
			}
		}
	}
	index, err := fs.ReadFile(frontend, "index.html")
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	// index.html names the current hashed assets, so it must never be cached.
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", index)
}

func contentType(name string) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// SPAHandler serves the built frontend with index.html as the fallback for deep
// links. It is also the Wails asset handler of the desktop.
func SPAHandler(frontend fs.FS) http.Handler {
	files := http.FileServer(http.FS(frontend))
	index, indexErr := fs.ReadFile(frontend, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, err := http.FS(frontend).Open(r.URL.Path); err == nil {
			_ = f.Close()
			files.ServeHTTP(w, r)
			return
		}
		// http.FileServer redirects any path ending in /index.html to ./, which
		// sends deep links such as /sessions/1 into a redirect loop.
		if indexErr != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

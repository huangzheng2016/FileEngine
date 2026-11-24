package app

import (
	"context"
	"io/fs"
	"net/http"
	"strings"

	"FileEngine/biz/errs"
	"FileEngine/biz/handler"
	"FileEngine/biz/handler/resp"
	"FileEngine/biz/router"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/adaptor"
)

// registerRoutes mounts every /api route from the IDL, the health probe and the
// embedded frontend. Nothing under /api is registered by hand.
func registerRoutes(h *server.Hertz, frontend fs.FS) {
	router.GeneratedRegister(h)
	h.GET("/healthz", handler.Healthz)

	var spa app.HandlerFunc
	if frontend != nil {
		spa = adaptor.HertzHandler(SPAHandler(frontend))
	}
	h.NoRoute(func(ctx context.Context, c *app.RequestContext) {
		if spa == nil || string(c.Path()) == "/api" || strings.HasPrefix(string(c.Path()), "/api/") {
			resp.Fail(ctx, c, errs.NoSuchRoute)
			return
		}
		spa(ctx, c)
	})
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

package main

import (
	"context"
	"net"
	"net/http"
	"time"

	"FileEngine/internal/app"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Service ties the in-process FileEngine server to the Wails lifecycle: it
// starts serving on the pre-bound loopback listener when Wails starts and
// shuts the server and database down when the application quits. It exposes
// no bound methods; the UI talks to the server over HTTP.
type Service struct {
	app     *app.App
	ln      net.Listener
	handler http.Handler
}

func (s *Service) ServiceName() string {
	return "FileEngine"
}

// ServiceStartup is a no-op when startup failed before the server was built
// (the startup error window is shown instead).
func (s *Service) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	if s.app == nil {
		return nil
	}
	return s.app.Start(context.Background(), s.ln, s.handler)
}

// serviceShutdownTimeout bounds the graceful HTTP shutdown. Request contexts
// are cancelled as soon as shutdown begins, so open SSE streams end at once;
// Wails runs ServiceShutdown on the main thread, so it must not hang.
const serviceShutdownTimeout = 5 * time.Second

func (s *Service) ServiceShutdown() error {
	if s.app == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), serviceShutdownTimeout)
	defer cancel()
	return s.app.Shutdown(ctx)
}

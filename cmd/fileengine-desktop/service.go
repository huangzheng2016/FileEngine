package main

import (
	"context"
	"net"
	"net/http"
	"time"

	"FileEngine/internal/app"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type Service struct {
	app     *app.App
	ln      net.Listener
	handler http.Handler
}

func (s *Service) ServiceName() string {
	return "FileEngine"
}

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

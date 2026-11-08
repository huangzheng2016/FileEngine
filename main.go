// Command fileengine is the headless server: REST API + SSE + embedded SPA on
// server.host:server.port. The desktop shell lives in cmd/fileengine-desktop.
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"FileEngine/internal/app"
	"FileEngine/internal/logging"
	"FileEngine/web"
)

const shutdownTimeout = 40 * time.Second

func main() {
	logging.Setup(os.Stderr)
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	frontend, err := web.Dist()
	if err != nil {
		log.Fatalf("Frontend not embedded: %v", err)
	}
	a, err := app.New(app.Options{ConfigPath: cfgPath, Frontend: frontend})
	if err != nil {
		log.Fatalf("Startup failed: %v", err)
	}

	ln, err := net.Listen("tcp", a.Addr())
	if err != nil {
		log.Fatalf("Listen on %s: %v", a.Addr(), err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.Start(context.Background(), ln, a.Handler()); err != nil {
		log.Fatalf("Start server: %v", err)
	}
	log.Printf("Starting FileEngine on %s", ln.Addr())

	served := make(chan error, 1)
	go func() { served <- a.Wait() }()
	select {
	case err := <-served:
		if err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	case <-ctx.Done():
		log.Printf("Shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := a.Shutdown(shutdownCtx); err != nil {
			log.Printf("Shutdown: %v", err)
		}
	}
}

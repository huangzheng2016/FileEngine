// Package app wires config, database and the HTTP API into one runnable unit
// shared by the server binary (main.go) and the desktop shell
// (cmd/fileengine-desktop). Both serve the same Gin handler from an
// in-process http.Server; only the listener and the request guard differ.
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"FileEngine/internal/api"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

// readHeaderTimeout bounds slow header writes. There is deliberately no
// read/write timeout: the SSE event stream is a long-lived response.
const readHeaderTimeout = 10 * time.Second

// Options configures New.
type Options struct {
	// ConfigPath is the config.yaml to load. It must exist.
	ConfigPath string
	// Frontend holds the built SPA (index.html at its root). Nil serves the
	// API only.
	Frontend fs.FS
}

// App is a loaded FileEngine instance: config applied, database migrated and
// the API router built. Start serves it; Shutdown stops serving and closes
// the database.
type App struct {
	cfg    *config.Config
	server *api.Server

	mu         sync.Mutex
	httpServer *http.Server
	served     chan error
	cancelReqs context.CancelFunc
}

// New loads the config, initializes the database and builds the API server.
func New(opts Options) (*App, error) {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", opts.ConfigPath, err)
	}
	if err := db.Init(cfg.Database); err != nil {
		return nil, fmt.Errorf("initialize database: %w", err)
	}
	return &App{
		cfg:    cfg,
		server: api.NewServer(db.NewRepository(db.GetDB()), opts.Frontend),
	}, nil
}

// Config returns the configuration loaded by New.
func (a *App) Config() *config.Config { return a.cfg }

// Handler returns the unguarded API + SPA handler.
func (a *App) Handler() http.Handler { return a.server.Handler() }

// Addr returns the configured server listen address (server.host:server.port).
func (a *App) Addr() string {
	return net.JoinHostPort(a.cfg.Server.Host, fmt.Sprint(a.cfg.Server.Port))
}

// Start serves handler on ln in the background. Request contexts derive from
// ctx and are cancelled when Shutdown begins, so long-lived SSE streams end
// instead of holding the graceful shutdown open. Wait reports the outcome.
func (a *App) Start(ctx context.Context, ln net.Listener, handler http.Handler) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.httpServer != nil {
		return errors.New("app: already started")
	}
	reqCtx, cancel := context.WithCancel(ctx)
	fresh := newFreshConns()
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return reqCtx },
		ConnState:         fresh.track,
	}
	srv.RegisterOnShutdown(cancel)
	// http.Server treats a connection that never sent a request as active for
	// 5s, so speculative client dials would stall Shutdown that long.
	srv.RegisterOnShutdown(fresh.closeAll)
	a.httpServer = srv
	a.cancelReqs = cancel
	a.served = make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		a.served <- err
	}()
	return nil
}

// Wait blocks until the server started by Start stops and returns its error
// (nil after a graceful Shutdown).
func (a *App) Wait() error {
	a.mu.Lock()
	served := a.served
	a.mu.Unlock()
	if served == nil {
		return errors.New("app: not started")
	}
	err := <-served
	served <- err // keep the result readable for later Wait calls
	return err
}

// Shutdown stops accepting requests, cancels in-flight request contexts and
// waits for handlers to return (bounded by ctx), then closes the database.
func (a *App) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	srv, cancel := a.httpServer, a.cancelReqs
	a.mu.Unlock()

	var errs []error
	if srv != nil {
		if err := srv.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("http shutdown: %w", err))
		}
		cancel()
	}
	if gdb := db.GetDB(); gdb != nil {
		if sqlDB, err := gdb.DB(); err == nil {
			if err := sqlDB.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close database: %w", err))
			}
		}
	}
	return errors.Join(errs...)
}

type freshConns struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func newFreshConns() *freshConns {
	return &freshConns{conns: make(map[net.Conn]struct{})}
}

func (f *freshConns) track(c net.Conn, state http.ConnState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if state == http.StateNew {
		f.conns[c] = struct{}{}
	} else {
		delete(f.conns, c)
	}
}

func (f *freshConns) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for c := range f.conns {
		_ = c.Close()
	}
}

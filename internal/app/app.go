// Package app wires config, database and the Hertz API into one unit shared by
// the server binary and the desktop shell. The server serves HTTP on a listener
// it is handed; the desktop never listens and dispatches in-process calls to
// the same engine (see inproc.go).
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"sync"
	"time"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/mw"
	"FileEngine/biz/service/auth"
	"FileEngine/container"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
	"FileEngine/internal/scanner"
	"FileEngine/internal/secret"

	"github.com/cloudwego/hertz/pkg/app/server"
	hzconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/hertz/pkg/network/standard"
	"github.com/cloudwego/hertz/pkg/route"
)

const (
	idleTimeout = 120 * time.Second
	readTimeout = 60 * time.Second
	// Bodies are checked per route by mw.BodyLimit; the server reads at most the
	// largest allowed body plus framing.
	maxBodyBytes = mw.ImportBodyLimit + 1<<20
	// Hertz waits this long for open connections after the registry closed the
	// streams, so only a stuck request can consume it.
	exitWait = time.Second
)

type Options struct {
	ConfigPath string
	// Nil serves the API only.
	Frontend fs.FS
	// Desktop runs without a token: calls arrive in-process (see Do) and the
	// access token is neither read from nor written to the config.
	Desktop    bool
	LocalRoots []string
}

type App struct {
	cfg  *config.Config
	opts Options

	mu     sync.Mutex
	hertz  *server.Hertz
	served chan error

	inprocOnce sync.Once
	inproc     *route.Engine
}

func New(opts Options) (*App, error) {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", opts.ConfigPath, err)
	}
	dataDir := config.DataDir(cfg)
	keyFile, err := secret.Init(dataDir)
	if err != nil {
		return nil, fmt.Errorf("load secret key: %w", err)
	}
	denied := []string{dataDir, config.Path(), keyFile}
	if cfg.Database.Driver == "sqlite" {
		denied = append(denied, cfg.Database.DSN)
	}
	if err := remotefs.SetLocalPolicy(remotefs.LocalPolicy{
		Roots:  append(remotefs.LocalRootsFromEnv(), opts.LocalRoots...),
		Denied: denied,
	}); err != nil {
		return nil, fmt.Errorf("local path policy: %w", err)
	}
	if err := db.Open(cfg.Database); err != nil {
		return nil, fmt.Errorf("initialize database: %w", err)
	}
	token, err := accessToken(opts)
	if err != nil {
		return nil, err
	}
	if err := container.Init(dal.DB(), dal.ReadDB(), auth.Options{Token: token, BindHost: cfg.Server.Host}); err != nil {
		return nil, fmt.Errorf("build container: %w", err)
	}
	repo := container.Default.Repo
	scanner.ApplyRulesHook = func(sessionID uint) {
		if err := repo.PrepareSession(sessionID); err != nil {
			slog.Error("apply rules after scan", "session", sessionID, "err", err)
		}
	}
	hlog.SetLevel(hlog.LevelWarn)
	return &App{cfg: cfg, opts: opts}, nil
}

func accessToken(opts Options) (string, error) {
	if opts.Desktop {
		return auth.NewToken()
	}
	t, generated, err := config.EnsureToken()
	if err != nil {
		return "", fmt.Errorf("api token: %w", err)
	}
	if generated {
		// Printed exactly once; afterwards it lives only in config.yaml.
		log.Printf("Generated API access token (shown once, stored as server.token in %s): %s", config.Path(), t)
	}
	return t, nil
}

func (a *App) Config() *config.Config { return a.cfg }

// Addr is the configured listen address.
func (a *App) Addr() string {
	return net.JoinHostPort(a.cfg.Server.Host, fmt.Sprint(a.cfg.Server.Port))
}

// BootstrapURL is the one-click login link for origin (server mode).
func (a *App) BootstrapURL(origin string) string {
	return container.Default.Auth.BootstrapURL(origin)
}

// newHertz builds the engine. Always the standard transport: it works on every
// OS, including Windows, and takes a pre-opened listener.
func (a *App) newHertz(extra ...hzconfig.Option) *server.Hertz {
	opts := []hzconfig.Option{
		server.WithTransport(standard.NewTransporter),
		server.WithMaxRequestBodySize(maxBodyBytes),
		server.WithReadTimeout(readTimeout),
		server.WithIdleTimeout(idleTimeout),
		server.WithExitWaitTime(exitWait),
		server.WithDisablePrintRoute(true),
	}
	h := server.New(append(opts, extra...)...)
	h.Use(mw.AccessLog(), mw.Recover(), mw.SecurityHeaders(), mw.HostGuard())
	registerRoutes(h, a.opts.Frontend)
	return h
}

// Start serves HTTP on ln until Shutdown. It returns once serving has begun.
func (a *App) Start(ln net.Listener) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.hertz != nil {
		return errors.New("app: already started")
	}
	h := a.newHertz(server.WithListener(ln))
	a.hertz = h
	a.served = make(chan error, 1)
	go func() { a.served <- h.Run() }()
	return nil
}

// Wait blocks until the server stops serving.
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

// Shutdown stops everything in dependency order: open event streams first (they
// would otherwise hold the HTTP shutdown open for its whole budget), then runs
// and scans, then the HTTP server, then the database.
func (a *App) Shutdown(ctx context.Context) error {
	var errs []error
	if err := container.Default.Streams.Close(ctx); err != nil {
		errs = append(errs, fmt.Errorf("close event streams: %w", err))
	}
	if err := container.Default.Runner.Shutdown(ctx); err != nil {
		errs = append(errs, err)
	}
	a.mu.Lock()
	h := a.hertz
	a.mu.Unlock()
	if h != nil {
		if err := h.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("http shutdown: %w", err))
		}
	}
	if sqlDB, err := container.Default.DB.DB(); err == nil {
		if err := sqlDB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close database: %w", err))
		}
	}
	return errors.Join(errs...)
}

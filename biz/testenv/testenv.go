// Package testenv builds the whole backend over a temporary SQLite database for
// tests: config, container and the Hertz engine with every IDL route. Handler
// and middleware tests drive it with hertz ut, no listener involved.
package testenv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"FileEngine/biz/mw"
	"FileEngine/internal/app"
	"FileEngine/internal/config"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/route"
)

type Env struct {
	App    *app.App
	Engine *route.Engine
	// Token is the access token of this instance.
	Token string
}

// New starts a fresh backend; it is shut down with the test. Tests of one
// process share the package-level globals (config, database, container), so
// they must not run in parallel.
func New(t testing.TB) *Env {
	t.Helper()
	mw.ResetRateLimits()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "database:\n  driver: sqlite\n  dsn: " + filepath.ToSlash(filepath.Join(dir, "fe.db")) + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	frontend := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>fe-test</title>")}}
	a, err := app.New(app.Options{ConfigPath: cfgPath, Frontend: frontend})
	if err != nil {
		t.Fatalf("start backend: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Shutdown(ctx)
	})
	return &Env{App: a, Engine: a.Engine(), Token: config.Get().Server.Token}
}

// Bearer is the Authorization header of an authenticated caller.
func (e *Env) Bearer() ut.Header {
	return ut.Header{Key: "Authorization", Value: "Bearer " + e.Token}
}

// Request performs method path against the engine. body is the JSON text of a
// POST ("" for none). Add e.Bearer() for an authenticated call.
func (e *Env) Request(method, path, body string, headers ...ut.Header) *ut.ResponseRecorder {
	var b *ut.Body
	if body != "" {
		headers = append(headers, ut.Header{Key: "Content-Type", Value: "application/json"})
		b = &ut.Body{Body: strings.NewReader(body), Len: len(body)}
	}
	return ut.PerformRequest(e.Engine, method, "http://127.0.0.1"+path, b, headers...)
}

package api

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"FileEngine/internal/agent"
	"FileEngine/internal/db"
	"FileEngine/internal/executor"

	"github.com/gin-gonic/gin"
)

// GET /healthz (unauthenticated): 200 when the database answers a ping.
func (s *Server) healthz(c *gin.Context) {
	sqlDB, err := s.repo.DB().DB()
	if err == nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		err = sqlDB.PingContext(ctx)
	}
	if err != nil {
		slog.Error("healthz: database ping failed", "err", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// POST /api/v1/admin/backup streams a consistent SQLite snapshot taken with
// VACUUM INTO (safe while the app keeps writing).
func (s *Server) backup(c *gin.Context) {
	if s.repo.DB().Dialector.Name() != "sqlite" {
		badRequest(c, "仅 SQLite 数据库支持在线备份，MySQL/PostgreSQL 请使用数据库自带工具")
		return
	}
	dir, err := os.MkdirTemp("", "fileengine-backup-*")
	if err != nil {
		internalError(c, err)
		return
	}
	defer os.RemoveAll(dir)
	snap := filepath.Join(dir, "snapshot.db")
	if err := s.repo.DB().Exec("VACUUM INTO ?", snap).Error; err != nil {
		internalError(c, fmt.Errorf("vacuum into: %w", err))
		return
	}
	name := "fileengine-" + time.Now().Format("20060102-150405") + ".db"
	c.Header("Content-Disposition", contentDisposition("attachment", name))
	c.Header("Cache-Control", "no-store")
	c.File(snap)
}

// drainTimeout bounds how long Shutdown waits for runs and executors.
const drainTimeout = 30 * time.Second

// Shutdown stops all running work so the process can exit cleanly: run
// contexts are cancelled, legacy agents are stopped, and executors are asked
// to stop after their current file operation. It waits for those to drain,
// bounded by ctx. The HTTP server itself is shut down by the caller.
func (s *Server) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, drainTimeout)
	defer cancel()
	if n := db.CancelAllRuns(); n > 0 {
		slog.Info("shutdown: cancelled runs", "count", n)
	}

	agentMu.Lock()
	var running []*agent.Agent
	for _, m := range []map[uint]*agent.Agent{agents, instructAgents} {
		for _, a := range m {
			if a.IsRunning() {
				a.Stop()
				running = append(running, a)
			}
		}
	}
	agentMu.Unlock()

	execMu.Lock()
	var execs []*executor.Executor
	for _, e := range executors {
		if e.IsRunning() {
			e.Stop()
			execs = append(execs, e)
		}
	}
	execMu.Unlock()

	for _, a := range running {
		select {
		case <-a.Done():
		case <-ctx.Done():
			return fmt.Errorf("agents did not stop: %w", ctx.Err())
		}
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for _, e := range execs {
		for e.IsRunning() {
			select {
			case <-tick.C:
			case <-ctx.Done():
				return fmt.Errorf("executor did not finish its current operation: %w", ctx.Err())
			}
		}
	}
	return nil
}

// contentDisposition builds a header value that cannot be broken by quotes,
// CR/LF or non-ASCII in the file name; unencodable names fall back to the
// bare disposition.
func contentDisposition(disposition, filename string) string {
	if v := mime.FormatMediaType(disposition, map[string]string{"filename": filename}); v != "" {
		return v
	}
	return disposition
}

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

	"FileEngine/internal/db"

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

// drainTimeout bounds how long Shutdown waits for runs to finish.
const drainTimeout = 30 * time.Second

// Shutdown cancels every running run (agent and executor runs alike; the
// executor stops before its next step and never mid-delete) and waits until
// they have all finished, bounded by drainTimeout and ctx. The HTTP server
// itself is shut down by the caller.
func (s *Server) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, drainTimeout)
	defer cancel()
	if n := db.CancelAllRuns(); n > 0 {
		slog.Info("shutdown: cancelled runs", "count", n)
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for db.LiveRunCount() > 0 {
		select {
		case <-tick.C:
		case <-ctx.Done():
			return fmt.Errorf("%d run(s) did not finish: %w", db.LiveRunCount(), ctx.Err())
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

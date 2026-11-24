// Package handler holds the handlers that are not part of the IDL.
package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"FileEngine/container"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
)

// Healthz answers 200 only when the database answers; it needs no token.
func Healthz(ctx context.Context, c *app.RequestContext) {
	sqlDB, err := container.Default.DB.DB()
	if err == nil {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		err = sqlDB.PingContext(pingCtx)
	}
	if err != nil {
		slog.Error("healthz: database ping failed", "err", err)
		c.JSON(http.StatusServiceUnavailable, utils.H{"status": "unhealthy"})
		return
	}
	c.JSON(http.StatusOK, utils.H{"status": "ok"})
}

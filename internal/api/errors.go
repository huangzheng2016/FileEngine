package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Error envelope: every error response is {"error": "<message in Chinese>"}. Client
// errors carry a message meant for the user; internal errors are logged with
// their detail server-side and answered with a fixed message so DB errors,
// paths and driver internals never leak to the client.

const msgInternal = "服务器内部错误"

// abortError writes the envelope and aborts the handler chain.
func abortError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

func badRequest(c *gin.Context, msg string) { abortError(c, http.StatusBadRequest, msg) }

func notFound(c *gin.Context, msg string) { abortError(c, http.StatusNotFound, msg) }

// internalError logs err with request context and answers 500 generically.
func internalError(c *gin.Context, err error) {
	slog.Error("internal error", "method", c.Request.Method, "path", c.Request.URL.Path, "err", err)
	abortError(c, http.StatusInternalServerError, msgInternal)
}

var errConfigNotLoaded = errors.New("config not loaded")

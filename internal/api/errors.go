package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
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

func conflict(c *gin.Context, msg string) { abortError(c, http.StatusConflict, msg) }

// Messages shared by many handlers.
const (
	msgBadBody      = "请求参数缺失或格式错误"
	msgBadID        = "无效的 ID"
	msgNoSession    = "会话不存在"
	msgNoFile       = "文件不存在"
	msgNoRun        = "运行不存在"
	msgNoCategory   = "分类不存在"
	msgNoModel      = "模型不存在"
	msgNoFilesystem = "文件系统不存在"
	msgFSConnect    = "无法连接文件系统"
)

// badBody answers a request body that failed to bind; the binder's English
// detail is only logged.
func badBody(c *gin.Context, err error) {
	slog.Debug("bad request body", "method", c.Request.Method, "path", c.Request.URL.Path, "err", err)
	badRequest(c, msgBadBody)
}

// lookupFailed answers a failed single-row lookup: a missing row is a 404
// with msg, anything else an internal error.
func lookupFailed(c *gin.Context, err error, msg string) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		notFound(c, msg)
		return
	}
	internalError(c, err)
}

// internalError logs err with request context and answers 500 generically.
func internalError(c *gin.Context, err error) {
	slog.Error("internal error", "method", c.Request.Method, "path", c.Request.URL.Path, "err", err)
	abortError(c, http.StatusInternalServerError, msgInternal)
}

var errConfigNotLoaded = errors.New("config not loaded")

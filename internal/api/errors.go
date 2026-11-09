package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Internal errors are logged server-side and answered with a fixed message
// so DB errors, paths and driver internals never leak to the client.

const msgInternal = "服务器内部错误"

func abortError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

func badRequest(c *gin.Context, msg string) { abortError(c, http.StatusBadRequest, msg) }

func notFound(c *gin.Context, msg string) { abortError(c, http.StatusNotFound, msg) }

func conflict(c *gin.Context, msg string) { abortError(c, http.StatusConflict, msg) }

const (
	msgBadBody      = "请求参数缺失或格式错误"
	msgBadID        = "无效的 ID"
	msgNoSession    = "整理任务不存在"
	msgBadSessionID = "无效的任务 ID"
	msgNoFile       = "文件不存在"
	msgNoRun        = "运行不存在"
	msgNoCategory   = "分类不存在"
	msgNoModel      = "模型不存在"
	msgNoFilesystem = "存储不存在"
	msgFSConnect    = "无法连接存储"
)

func badBody(c *gin.Context, err error) {
	slog.Debug("bad request body", "method", c.Request.Method, "path", c.Request.URL.Path, "err", err)
	badRequest(c, msgBadBody)
}

func lookupFailed(c *gin.Context, err error, msg string) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		notFound(c, msg)
		return
	}
	internalError(c, err)
}

func internalError(c *gin.Context, err error) {
	slog.Error("internal error", "method", c.Request.Method, "path", c.Request.URL.Path, "err", err)
	abortError(c, http.StatusInternalServerError, msgInternal)
}

var errConfigNotLoaded = errors.New("config not loaded")

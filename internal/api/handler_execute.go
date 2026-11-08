package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"FileEngine/internal/db"
	"FileEngine/internal/executor"
	"FileEngine/internal/remotefs"

	"github.com/gin-gonic/gin"
)

func (s *Server) sessionFS(c *gin.Context, sessionID uint) (remotefs.RemoteFS, bool) {
	session, err := s.repo.GetSession(sessionID)
	if err != nil {
		lookupFailed(c, err, msgNoSession)
		return nil, false
	}
	fsCfg, err := s.resolveFSConfig(session)
	if err != nil {
		fsConfigFailed(c, err)
		return nil, false
	}
	fs, err := remotefs.NewFromConfig(fsCfg)
	if err != nil {
		slog.Warn("connect filesystem", "session", sessionID, "err", err)
		abortError(c, http.StatusServiceUnavailable, msgFSConnect)
		return nil, false
	}
	return fs, true
}

func runStartError(c *gin.Context, err error) {
	if errors.Is(err, db.ErrSessionBusy) {
		conflict(c, err.Error())
		return
	}
	internalError(c, err)
}

func (s *Server) startExecute(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}
	var opts executor.Options
	if err := c.ShouldBindJSON(&opts); err != nil {
		badBody(c, err)
		return
	}
	if opts.Mode != executor.ModeCopy && opts.Mode != executor.ModeMove {
		badRequest(c, "执行模式只能是 copy 或 move")
		return
	}
	fs, ok := s.sessionFS(c, uint(id))
	if !ok {
		return
	}
	run, err := executor.Start(s.repo, fs, uint(id), opts)
	if err != nil {
		fs.Close()
		runStartError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_id": run.ID})
}

func (s *Server) checkExecute(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}
	sample, _ := strconv.Atoi(c.Query("sample"))
	opts := executor.Options{Mode: c.DefaultQuery("mode", executor.ModeCopy), Sample: sample, CategoryPath: c.Query("category_path")}
	fs, ok := s.sessionFS(c, uint(id))
	if !ok {
		return
	}
	defer fs.Close()
	res, err := executor.New(s.repo, fs).Validate(c.Request.Context(), uint(id), opts)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (s *Server) revertRun(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("run_id"), 10, 32)
	if err != nil {
		badRequest(c, "无效的运行 ID")
		return
	}
	var target db.AgentRun
	if err := s.repo.DB().First(&target, id).Error; err != nil {
		lookupFailed(c, err, msgNoRun)
		return
	}
	if target.Phase != db.RunPhaseExecuting {
		badRequest(c, "只能撤销执行运行")
		return
	}
	fs, ok := s.sessionFS(c, target.SessionID)
	if !ok {
		return
	}
	run, err := executor.StartRevert(s.repo, fs, uint(id))
	if err != nil {
		fs.Close()
		runStartError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"run_id": run.ID})
}

func (s *Server) locateEntries(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}
	q := c.Query("q")
	if q == "" {
		badRequest(c, "请输入要查找的文件名或路径")
		return
	}
	items, err := executor.Locate(s.repo, uint(id), q, 50)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

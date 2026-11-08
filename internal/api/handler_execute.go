package api

import (
	"errors"
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
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return nil, false
	}
	fsCfg, err := s.resolveFSConfig(session)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, false
	}
	fs, err := remotefs.NewFromConfig(fsCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connect fs: " + err.Error()})
		return nil, false
	}
	return fs, true
}

func runStartError(c *gin.Context, err error) {
	if errors.Is(err, db.ErrSessionBusy) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

// startExecute: POST /sessions/:id/execute {mode, sample?, category_path?} → {run_id}.
func (s *Server) startExecute(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var opts executor.Options
	if err := c.ShouldBindJSON(&opts); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if opts.Mode != executor.ModeCopy && opts.Mode != executor.ModeMove {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mode must be 'copy' or 'move'"})
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

// checkExecute: GET /sessions/:id/execute/check?mode=&sample=&category_path=
// → executor.Result (validation only, nothing is changed).
func (s *Server) checkExecute(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// revertRun: POST /runs/:run_id/revert → {run_id} of the new reverting run.
func (s *Server) revertRun(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("run_id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run id"})
		return
	}
	var target db.AgentRun
	if err := s.repo.DB().First(&target, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
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

// locateEntries: GET /sessions/:id/locate?q= → {items: LocateItem[]}.
func (s *Server) locateEntries(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	q := c.Query("q")
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "q is required"})
		return
	}
	items, err := executor.Locate(s.repo, uint(id), q, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"FileEngine/internal/agent"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/executor"
	"FileEngine/internal/remotefs"

	"github.com/gin-gonic/gin"
)

var (
	agentMu   sync.Mutex
	agents    = make(map[uint]*agent.Agent)
	execMu    sync.Mutex
	executors = make(map[uint]*executor.Executor)
)

// resolveFSConfig resolves the RemoteFSConfig for a session via its Filesystem entity.
func (s *Server) resolveFSConfig(session *db.ScanSession) (config.RemoteFSConfig, error) {
	if session.FilesystemID == 0 {
		return config.RemoteFSConfig{}, fmt.Errorf("session has no filesystem_id")
	}
	filesystem, err := s.repo.GetFilesystem(session.FilesystemID)
	if err != nil {
		return config.RemoteFSConfig{}, fmt.Errorf("filesystem not found: %w", err)
	}
	fsCfg := filesystem.ToRemoteFSConfig()
	return fsCfg, nil
}

func (s *Server) startTagging(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	sessionID := uint(id)
	session, err := s.repo.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	agentMu.Lock()
	if a, ok := agents[sessionID]; ok && a.IsRunning() {
		agentMu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "tagging already running"})
		return
	}

	fsCfg, err := s.resolveFSConfig(session)
	if err != nil {
		agentMu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cfg := *config.Get() // copy to avoid mutating global
	cfg.Agent.AllowAutoCategory = session.AllowAutoCategory
	cfg.Agent.AllowReadFile = session.AllowReadFile

	// Resolve model provider from session or fall back to global config
	var modelProvider *db.ModelProvider
	if session.ModelProviderID > 0 {
		modelProvider, err = s.repo.GetModelProvider(session.ModelProviderID)
		if err != nil {
			agentMu.Unlock()
			c.JSON(http.StatusBadRequest, gin.H{"error": "model provider not found"})
			return
		}
	}

	a := agent.New(s.repo, fsCfg, &cfg, sessionID, modelProvider)
	agents[sessionID] = a
	agentMu.Unlock()

	go func() {
		if err := a.RunTagging(context.Background()); err != nil {
			session, _ := s.repo.GetSession(sessionID)
			if session != nil && session.Status == "tagging" {
				session.Status = "error: " + err.Error()
				s.repo.UpdateSession(session)
			}
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "tagging started", "session_id": sessionID})
}

func (s *Server) stopTagging(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	agentMu.Lock()
	a, ok := agents[uint(id)]
	agentMu.Unlock()

	if !ok || !a.IsRunning() {
		c.JSON(http.StatusOK, gin.H{"message": "not running"})
		return
	}
	a.Stop()
	c.JSON(http.StatusOK, gin.H{"message": "stopping"})
}

func (s *Server) tagStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	_ = s.repo.RefreshSessionCounts(uint(id))
	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	agentMu.Lock()
	a, ok := agents[uint(id)]
	agentMu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"status":       session.Status,
		"running":      ok && a.IsRunning(),
		"total_files":  session.TotalFiles,
		"tagged_files": session.TaggedFiles,
		"planned_ops":  session.PlannedOps,
	})
}

func (s *Server) startExecute(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// mode: "copy" (default) or "move"
	mode := c.DefaultQuery("mode", "copy")
	if mode != "copy" && mode != "move" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mode must be 'copy' or 'move'"})
		return
	}

	sessionID := uint(id)
	session, err := s.repo.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	fsCfg, err := s.resolveFSConfig(session)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	fs, err := remotefs.NewFromConfig(fsCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connect fs: " + err.Error()})
		return
	}

	execMu.Lock()
	if e, ok := executors[sessionID]; ok && e.IsRunning() {
		execMu.Unlock()
		fs.Close()
		c.JSON(http.StatusConflict, gin.H{"error": "execution already running"})
		return
	}

	e := executor.New(s.repo, fs)
	executors[sessionID] = e
	execMu.Unlock()

	go func() {
		defer fs.Close()
		if err := e.Execute(context.Background(), sessionID, mode); err != nil {
			session, _ := s.repo.GetSession(sessionID)
			if session != nil && session.Status == "executing" {
				session.Status = "error: " + err.Error()
				s.repo.UpdateSession(session)
			}
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "execution started"})
}

func (s *Server) stopExecute(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	execMu.Lock()
	e, ok := executors[uint(id)]
	execMu.Unlock()

	if !ok || !e.IsRunning() {
		c.JSON(http.StatusOK, gin.H{"message": "not running"})
		return
	}
	e.Stop()
	c.JSON(http.StatusOK, gin.H{"message": "stopping"})
}

func (s *Server) executeStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	execMu.Lock()
	e, ok := executors[uint(id)]
	execMu.Unlock()

	session, _ := s.repo.GetSession(uint(id))

	result := gin.H{
		"status":  session.Status,
		"running": ok && e.IsRunning(),
	}
	if ok {
		progress := e.Progress()
		result["total"] = progress.Total
		result["success"] = progress.Success
		result["failed"] = progress.Failed
		result["skipped"] = progress.Skipped
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) getPlans(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// DryRun only reads from DB, no FS connection needed
	e := executor.New(s.repo, nil)
	plans, err := e.DryRun(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, plans)
}

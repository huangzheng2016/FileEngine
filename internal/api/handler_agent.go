package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"FileEngine/internal/agent"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/executor"
	"FileEngine/internal/remotefs"

	"github.com/gin-gonic/gin"
)

var (
	agentMu        sync.Mutex
	agents         = make(map[uint]*agent.Agent)
	instructAgents = make(map[uint]*agent.Agent)
	execMu         sync.Mutex
	executors      = make(map[uint]*executor.Executor)
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

	cfg := config.Get()

	// Resolve model provider — required
	if session.ModelProviderID == 0 {
		agentMu.Unlock()
		c.JSON(http.StatusBadRequest, gin.H{"error": "no model provider configured for this session"})
		return
	}
	modelProvider, err := s.repo.GetModelProvider(session.ModelProviderID)
	if err != nil {
		agentMu.Unlock()
		c.JSON(http.StatusBadRequest, gin.H{"error": "model provider not found"})
		return
	}

	a := agent.New(s.repo, fsCfg, cfg, sessionID, modelProvider, session)
	agents[sessionID] = a
	agentMu.Unlock()

	go func() {
		defer func() {
			agentMu.Lock()
			delete(agents, sessionID)
			agentMu.Unlock()
		}()
		// The request ctx is canceled as soon as this handler returns; background
		// work runs on context.Background() (Stop() has its own cancelFn).
		report, err := a.RunTagging(context.Background())
		switch {
		case err == nil:
			publishRunDone(sessionID, db.PhaseTagging, db.StatusDone, report)
		case errors.Is(err, context.Canceled):
			publishRunDone(sessionID, db.PhaseTagging, db.StatusStopped, "")
		default:
			log.Printf("tagging session %d failed: %v", sessionID, err)
			session, _ := s.repo.GetSession(sessionID)
			if session != nil && session.Status == "tagging" {
				session.Status = db.TruncateStatus("error: " + err.Error())
				s.repo.UpdateSession(session)
			}
			publishRunDone(sessionID, db.PhaseTagging, db.StatusError, err.Error())
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "tagging started", "session_id": sessionID})
}

type InstructRequest struct {
	FileIDs []uint `json:"file_ids" binding:"required"`
	Prompt  string `json:"prompt" binding:"required"`
}

func (s *Server) instructAgent(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req InstructRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sessionID := uint(id)
	session, err := s.repo.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if session.ModelProviderID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no model provider configured"})
		return
	}
	modelProvider, err := s.repo.GetModelProvider(session.ModelProviderID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model provider not found"})
		return
	}

	// Fetch selected files
	var files []db.FileEntry
	for _, fid := range req.FileIDs {
		f, err := s.repo.GetFile(fid)
		if err != nil {
			continue
		}
		files = append(files, *f)
	}
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no valid files selected"})
		return
	}

	fsCfg, err := s.resolveFSConfig(session)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cfg := config.Get()
	a := agent.New(s.repo, fsCfg, cfg, sessionID, modelProvider, session)

	agentMu.Lock()
	if _, ok := instructAgents[sessionID]; ok {
		agentMu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "instruct already running"})
		return
	}
	instructAgents[sessionID] = a
	agentMu.Unlock()

	go func() {
		defer func() {
			agentMu.Lock()
			delete(instructAgents, sessionID)
			agentMu.Unlock()
		}()
		// The request ctx is canceled as soon as this handler returns; background
		// work runs on context.Background() (Stop() has its own cancelFn,
		// registered by RunInstruct like RunTagging). User-initiated stops
		// surface as context.Canceled and are not failures.
		resp, err := a.RunInstruct(context.Background(), files, req.Prompt)
		switch {
		case err == nil:
			publishRunDone(sessionID, db.PhaseInstruct, db.StatusDone, resp)
		case errors.Is(err, context.Canceled):
			publishRunDone(sessionID, db.PhaseInstruct, db.StatusStopped, "")
		default:
			log.Printf("instruct session %d failed: %v", sessionID, err)
			publishRunDone(sessionID, db.PhaseInstruct, db.StatusError, err.Error())
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "instruct started"})
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
	// Wait for the old run to drain: starting a new run on top of a
	// still-draining one is what left live runs invisible to this endpoint
	// (the drain's deferred cleanup is identity-guarded, but the session
	// should only read "stopped" once workers actually exited).
	select {
	case <-a.Done():
	case <-time.After(30 * time.Second):
		log.Printf("stopTagging session %d: run did not drain within 30s", id)
	}
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

func (s *Server) validatePlans(c *gin.Context) {
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
	defer fs.Close()

	e := executor.New(s.repo, fs)
	result, err := e.Validate(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) stopInstruct(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	agentMu.Lock()
	a, ok := instructAgents[uint(id)]
	agentMu.Unlock()

	if !ok || !a.IsRunning() {
		c.JSON(http.StatusOK, gin.H{"message": "not running"})
		return
	}
	a.Stop()
	// Wait for the old run to drain so a follow-up instruct never starts on
	// top of a still-draining one (its deferred cleanup is identity-guarded).
	select {
	case <-a.Done():
	case <-time.After(30 * time.Second):
		log.Printf("stopInstruct session %d: run did not drain within 30s", id)
	}
	c.JSON(http.StatusOK, gin.H{"message": "stopping"})
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
		defer func() {
			execMu.Lock()
			delete(executors, sessionID)
			execMu.Unlock()
			fs.Close()
		}()
		publishRunStatus(sessionID, db.PhaseExecuting, db.StatusStarted, "")

		// Bridge executor progress onto the session bus: the executor keeps
		// in-memory counters only, so poll them while it runs.
		progressDone := make(chan struct{})
		defer close(progressDone)
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-progressDone:
					return
				case <-ticker.C:
					publishExecProgress(sessionID, e.Progress())
				}
			}
		}()

		// The request ctx is canceled as soon as this handler returns; background
		// work runs on context.Background() (Stop() has its own cancelFn).
		err := e.Execute(context.Background(), sessionID, mode)
		publishExecProgress(sessionID, e.Progress()) // final counters
		switch {
		case err == nil:
			publishRunStatus(sessionID, db.PhaseExecuting, db.StatusDone, "")
			publishRunDone(sessionID, db.PhaseExecuting, db.StatusDone, "")
		case errors.Is(err, context.Canceled):
			// User-initiated stop: not a failure — restore the pre-execution
			// status (mirrors the tagging path) instead of "error: ...".
			session, _ := s.repo.GetSession(sessionID)
			if session != nil && session.Status == "executing" {
				session.Status = "tagged"
				s.repo.UpdateSession(session)
			}
			publishRunStatus(sessionID, db.PhaseExecuting, db.StatusStopped, "")
			publishRunDone(sessionID, db.PhaseExecuting, db.StatusStopped, "")
		default:
			log.Printf("execution session %d failed: %v", sessionID, err)
			session, _ := s.repo.GetSession(sessionID)
			if session != nil && session.Status == "executing" {
				session.Status = db.TruncateStatus("error: " + err.Error())
				s.repo.UpdateSession(session)
			}
			publishRunStatus(sessionID, db.PhaseExecuting, db.StatusError, err.Error())
			publishRunDone(sessionID, db.PhaseExecuting, db.StatusError, err.Error())
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

// publishExecProgress emits a progress event with the executor's counters.
func publishExecProgress(sessionID uint, p executor.ExecuteResult) {
	db.PublishEvent(db.Event{Type: db.EventProgress, SessionID: sessionID, Payload: db.ProgressPayload{
		SessionID: sessionID,
		Phase:     db.PhaseExecuting,
		Total:     p.Total,
		Success:   p.Success,
		Failed:    p.Failed,
		Skipped:   p.Skipped,
	}})
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

func (s *Server) exportPlans(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	e := executor.New(s.repo, nil)
	plans, err := e.DryRun(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=plans_session_%d.csv", id))
	// BOM for Excel UTF-8 compatibility
	c.Writer.Write([]byte("\xEF\xBB\xBF"))
	c.Writer.WriteString("original_path,new_path,operation,file_type,name\n")
	for _, p := range plans {
		c.Writer.WriteString(fmt.Sprintf("%s,%s,%s,%s,%s\n",
			csvEscape(p.OriginalPath), csvEscape(p.NewPath),
			p.Operation, p.FileType, csvEscape(p.Name)))
	}
}

func csvEscape(s string) string {
	if len(s) == 0 {
		return s
	}
	needsQuote := false
	for _, c := range s {
		if c == ',' || c == '"' || c == '\n' || c == '\r' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return s
	}
	escaped := "\""
	for _, c := range s {
		if c == '"' {
			escaped += "\"\""
		} else {
			escaped += string(c)
		}
	}
	return escaped + "\""
}

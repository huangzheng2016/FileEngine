package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"FileEngine/internal/agent"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/executor"

	"github.com/gin-gonic/gin"
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

// agentForSession resolves everything an agent run of the session needs. On
// failure it has already answered the request.
func (s *Server) agentForSession(c *gin.Context) (*db.ScanSession, *agent.Agent, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的会话 ID"})
		return nil, nil, false
	}
	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在"})
		return nil, nil, false
	}
	if session.ModelProviderID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "会话未配置模型"})
		return nil, nil, false
	}
	modelProvider, err := s.repo.GetModelProvider(session.ModelProviderID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模型不存在"})
		return nil, nil, false
	}
	fsCfg, err := s.resolveFSConfig(session)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, nil, false
	}
	return session, agent.New(s.repo, fsCfg, session.ID, modelProvider, session), true
}

// startTagging starts a tagging run: POST /sessions/:id/tag → {run_id}.
func (s *Server) startTagging(c *gin.Context) {
	session, a, ok := s.agentForSession(c)
	if !ok {
		return
	}
	run, err := s.launchRun(session.ID, db.RunPhaseTagging, "自动整理", func(ctx context.Context, run *db.AgentRun) runOutcome {
		logger := agent.NewLogger(session.ID, run.ID, 0)
		report, partial, err := a.RunTagging(ctx, logger)
		return runOutcome{Summary: report, Partial: partial, Err: err, Tokens: logger.Tokens()}
	})
	respondRunStart(c, run, err)
}

type InstructRequest struct {
	FileIDs []uint `json:"file_ids" binding:"required"`
	Prompt  string `json:"prompt" binding:"required"`
}

// instructAgent starts a chat run over the selected files:
// POST /sessions/:id/instruct → {run_id}.
func (s *Server) instructAgent(c *gin.Context) {
	var req InstructRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	session, a, ok := s.agentForSession(c)
	if !ok {
		return
	}
	var files []db.FileEntry
	for _, fid := range req.FileIDs {
		if f, err := s.repo.GetFile(fid); err == nil && f.ScanSessionID == session.ID {
			files = append(files, *f)
		}
	}
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未选择有效的文件"})
		return
	}
	run, err := s.launchRun(session.ID, db.RunPhaseChat, req.Prompt, func(ctx context.Context, run *db.AgentRun) runOutcome {
		logger := agent.NewLogger(session.ID, run.ID, 0)
		resp, err := a.RunInstruct(ctx, logger, files, req.Prompt)
		return runOutcome{Summary: resp, Err: err, Tokens: logger.Tokens()}
	})
	respondRunStart(c, run, err)
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

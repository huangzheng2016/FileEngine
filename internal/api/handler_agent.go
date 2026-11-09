package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"FileEngine/internal/agent"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/executor"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var errSessionNoFilesystem = errors.New("整理任务所用的存储已删除")

func (s *Server) resolveFSConfig(session *db.ScanSession) (config.RemoteFSConfig, error) {
	if session.FilesystemID == 0 {
		return config.RemoteFSConfig{}, errSessionNoFilesystem
	}
	filesystem, err := s.repo.GetFilesystem(session.FilesystemID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return config.RemoteFSConfig{}, errSessionNoFilesystem
	}
	if err != nil {
		return config.RemoteFSConfig{}, err
	}
	return filesystem.ToRemoteFSConfig(), nil
}

func fsConfigFailed(c *gin.Context, err error) {
	if errors.Is(err, errSessionNoFilesystem) {
		badRequest(c, err.Error())
		return
	}
	internalError(c, err)
}

// On failure it has already answered the request.
func (s *Server) agentForSession(c *gin.Context) (*db.ScanSession, *agent.Agent, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadSessionID)
		return nil, nil, false
	}
	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		lookupFailed(c, err, msgNoSession)
		return nil, nil, false
	}
	a, err := s.agentFor(session)
	if err != nil {
		agentFailed(c, err)
		return nil, nil, false
	}
	return session, a, true
}

var (
	errSessionNoModel      = errors.New("整理任务没有选模型")
	errSessionModelDeleted = errors.New("整理任务所选的模型已删除")
)

func (s *Server) agentFor(session *db.ScanSession) (*agent.Agent, error) {
	if session.ModelProviderID == 0 {
		return nil, errSessionNoModel
	}
	provider, err := s.repo.GetModelProvider(session.ModelProviderID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errSessionModelDeleted
	}
	if err != nil {
		return nil, err
	}
	fsCfg, err := s.resolveFSConfig(session)
	if err != nil {
		return nil, err
	}
	return agent.New(s.repo, fsCfg, session.ID, provider, session), nil
}

func agentFailed(c *gin.Context, err error) {
	if errors.Is(err, errSessionNoModel) || errors.Is(err, errSessionModelDeleted) || errors.Is(err, errSessionNoFilesystem) {
		badRequest(c, err.Error())
		return
	}
	internalError(c, err)
}

func (s *Server) startTagging(c *gin.Context) {
	session, a, ok := s.agentForSession(c)
	if !ok {
		return
	}
	run, err := s.launchTagging(session.ID, a)
	respondRunStart(c, run, err)
}

func (s *Server) startTaggingRun(sessionID uint) (*db.AgentRun, error) {
	_, a, err := s.sessionAgent(sessionID)
	if err != nil {
		return nil, err
	}
	return s.launchTagging(sessionID, a)
}

func (s *Server) launchTagging(sessionID uint, a *agent.Agent) (*db.AgentRun, error) {
	return s.launchRun(sessionID, db.RunPhaseTagging, "整理", func(ctx context.Context, run *db.AgentRun) runOutcome {
		logger := agent.NewLogger(sessionID, run.ID, 0)
		report, partial, err := a.RunTagging(ctx, logger)
		return runOutcome{Summary: report, Partial: partial, Err: err, Tokens: logger.Tokens(),
			Then: func(status string) { s.startReviewAfterTagging(sessionID, run.ID, status) }}
	})
}

func (s *Server) getPlans(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}

	e := executor.New(s.repo, nil)
	plans, err := e.DryRun(uint(id))
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, plans)
}

func (s *Server) exportPlans(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}

	e := executor.New(s.repo, nil)
	plans, err := e.DryRun(uint(id))
	if err != nil {
		internalError(c, err)
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

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

var errSessionNoFilesystem = errors.New("会话关联的文件系统不存在")

// resolveFSConfig resolves the RemoteFSConfig for a session via its Filesystem entity.
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

// fsConfigFailed answers a failed resolveFSConfig.
func fsConfigFailed(c *gin.Context, err error) {
	if errors.Is(err, errSessionNoFilesystem) {
		badRequest(c, err.Error())
		return
	}
	internalError(c, err)
}

// agentForSession resolves everything an agent run of the session needs. On
// failure it has already answered the request.
func (s *Server) agentForSession(c *gin.Context) (*db.ScanSession, *agent.Agent, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, "无效的会话 ID")
		return nil, nil, false
	}
	session, err := s.repo.GetSession(uint(id))
	if err != nil {
		lookupFailed(c, err, msgNoSession)
		return nil, nil, false
	}
	if session.ModelProviderID == 0 {
		badRequest(c, "会话未配置模型")
		return nil, nil, false
	}
	modelProvider, err := s.repo.GetModelProvider(session.ModelProviderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			badRequest(c, "会话配置的模型不存在")
		} else {
			internalError(c, err)
		}
		return nil, nil, false
	}
	fsCfg, err := s.resolveFSConfig(session)
	if err != nil {
		fsConfigFailed(c, err)
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
		return runOutcome{Summary: report, Partial: partial, Err: err, Tokens: logger.Tokens(),
			Then: func(status string) { s.startReviewAfterTagging(session.ID, run.ID, status) }}
	})
	respondRunStart(c, run, err)
}

func (s *Server) getPlans(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		badRequest(c, msgBadID)
		return
	}

	// DryRun only reads from DB, no FS connection needed
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

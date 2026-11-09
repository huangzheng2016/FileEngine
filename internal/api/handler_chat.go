package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"FileEngine/internal/agent"
	"FileEngine/internal/db"

	"github.com/gin-gonic/gin"
)

const chatTitleMaxRunes = 60

type ChatRequest struct {
	Content string `json:"content"`
	FileIDs []uint `json:"file_ids"`
}

func (s *Server) postChat(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息不能为空"})
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
	run, err := s.launchRun(session.ID, db.RunPhaseChat, chatTitle(content), func(ctx context.Context, run *db.AgentRun) runOutcome {
		logger := agent.NewLogger(session.ID, run.ID, 0)
		answer, err := a.RunChat(ctx, logger, content, files)
		return runOutcome{Summary: answer, Err: err, Tokens: logger.Tokens()}
	})
	respondRunStart(c, run, err)
}

func chatTitle(content string) string {
	t := strings.Join(strings.Fields(content), " ")
	if r := []rune(t); len(r) > chatTitleMaxRunes {
		t = string(r[:chatTitleMaxRunes]) + "…"
	}
	return t
}

func (s *Server) listChat(c *gin.Context) {
	session, ok := s.loadSession(c)
	if !ok {
		return
	}
	turns, err := s.repo.ListChatTurns(session.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if turns == nil {
		turns = []db.ChatTurn{}
	}
	c.JSON(http.StatusOK, gin.H{"items": turns})
}

func (s *Server) sessionAgent(sessionID uint) (*db.ScanSession, *agent.Agent, error) {
	session, err := s.repo.GetSession(sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("session %d: %w", sessionID, err)
	}
	a, err := s.agentFor(session)
	if err != nil {
		return nil, nil, err
	}
	return session, a, nil
}

func (s *Server) startReviewAfterTagging(sessionID, taggingRunID uint, status string) {
	if status != db.RunDone && status != db.RunPartial {
		return
	}
	if _, err := s.RunReviewAfterTagging(sessionID, taggingRunID); err != nil {
		log.Printf("session %d: start review after tagging run %d: %v", sessionID, taggingRunID, err)
	}
}

func (s *Server) RunReviewAfterTagging(sessionID, taggingRunID uint) (*db.AgentRun, error) {
	_, a, err := s.sessionAgent(sessionID)
	if err != nil {
		return nil, err
	}
	tagging, err := s.repo.GetRun(taggingRunID)
	if err != nil {
		return nil, fmt.Errorf("tagging run %d: %w", taggingRunID, err)
	}
	return s.launchRun(sessionID, db.RunPhaseReview, "复查", func(ctx context.Context, run *db.AgentRun) runOutcome {
		logger := agent.NewLogger(sessionID, run.ID, 0)
		report, err := a.RunReview(ctx, logger, tagging)
		return runOutcome{Summary: report, Err: err, Tokens: logger.Tokens()}
	})
}

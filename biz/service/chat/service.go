// Package chat sends the user's questions to the session's agent and lists the
// conversation.
package chat

import (
	"context"
	"strings"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/errs"
	"FileEngine/biz/service/runner"
	"FileEngine/biz/service/sessionagent"
	"FileEngine/internal/agent"
	"FileEngine/internal/db"
)

const (
	titleMaxRunes = 60
	// MaxFiles is how many files one question can be about.
	MaxFiles       = 50
	maxContentRune = 20000
)

type Service struct {
	q      *query.Query
	sa     *sessionagent.Factory
	runner *runner.Service
}

func NewService(q *query.Query, sa *sessionagent.Factory, r *runner.Service) *Service {
	return &Service{q: q, sa: sa, runner: r}
}

// Send starts a chat run and returns its id; the reply streams as events.
// Files that do not belong to the session are ignored.
func (s *Service) Send(ctx context.Context, sessionID uint, content string, fileIDs []uint) (uint, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return 0, errs.Invalid("消息不能为空")
	}
	if len([]rune(content)) > maxContentRune {
		return 0, errs.Invalid("消息过长")
	}
	if len(fileIDs) > MaxFiles {
		return 0, errs.BadRequest
	}
	session, err := s.sa.Session(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	a, err := s.sa.Agent(ctx, session)
	if err != nil {
		return 0, err
	}
	var files []model.FileEntry
	if len(fileIDs) > 0 {
		f := s.q.FileEntry
		rows, err := f.WithContext(ctx).Where(f.ScanSessionID.Eq(sessionID), f.ID.In(fileIDs...)).Order(f.ID).Find()
		if err != nil {
			return 0, err
		}
		for _, r := range rows {
			files = append(files, *r)
		}
	}
	run, err := s.runner.Launch(sessionID, db.RunPhaseChat, title(content), func(ctx context.Context, run *model.AgentRun) runner.Outcome {
		logger := agent.NewLogger(sessionID, run.ID, 0)
		answer, err := a.RunChat(ctx, logger, content, files)
		return runner.Outcome{Summary: answer, Err: err, Tokens: logger.Tokens()}
	})
	if err != nil {
		return 0, sessionagent.StartError(err)
	}
	return run.ID, nil
}

func title(content string) string {
	t := strings.Join(strings.Fields(content), " ")
	if r := []rune(t); len(r) > titleMaxRunes {
		t = string(r[:titleMaxRunes]) + "…"
	}
	return t
}

// List is the conversation, oldest first; summary turns are internal.
func (s *Service) List(ctx context.Context, sessionID uint) ([]*model.ChatTurn, error) {
	if _, err := s.sa.Session(ctx, sessionID); err != nil {
		return nil, err
	}
	t := s.q.ChatTurn
	return t.WithContext(ctx).
		Where(t.SessionID.Eq(sessionID), t.Role.In(db.ChatRoleUser, db.ChatRoleAssistant)).
		Order(t.ID).Find()
}

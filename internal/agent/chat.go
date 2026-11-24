package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"
	"FileEngine/internal/remotefs"
)

var newChatModel = modelfactory.NewChatModelFromProvider

func SetModelFactory(f func(context.Context, *model.ModelProvider) (einomodel.ChatModel, error)) (restore func()) {
	old := newChatModel
	newChatModel = f
	return func() { newChatModel = old }
}

type mainRunSpec struct {
	label       string
	phase       string // db.RunPhase*: selects the handler gates
	maxStep     int
	instruction string
	// shared is rendered once into message 0; the run never re-renders it.
	shared SharedContext
	// current is the categories the model has been told about (the snapshot
	// plus any delta); it primes the create_category version guard.
	current []model.Category
	// messages follow the shared-context message: history, then this run's
	// own input last.
	messages []*schema.Message
}

func (a *Agent) runMainAgent(ctx context.Context, logger *Logger, spec mainRunSpec) (string, error) {
	ctx = modelfactory.WithPromptCacheKey(ctx, int64(a.sessionID), "main")
	chatModel, err := newChatModel(ctx, a.modelProvider)
	if err != nil {
		return "", fmt.Errorf("create model: %w", err)
	}
	fs, err := remotefs.NewFromConfig(a.fsCfg)
	if err != nil {
		return "", fmt.Errorf("connect fs: %w", err)
	}
	defer fs.Close()

	tracker := newTokenTrackingModel(chatModel)
	tb := NewToolBuilder(a.repo, fs, a.sessionID, a.session.FilesystemID, logger, a.cfg, a.session, a.modelProvider.VisionEnabled)
	tb.RunID = logger.RunID()
	tb.RunKind = spec.phase
	tb.guard.note(categoryNames(spec.current))
	tools, err := tb.BuildChatTools()
	if err != nil {
		return "", fmt.Errorf("build tools: %w", err)
	}
	summaryOpt, err := bulkSummaryOption(ctx, a.repo, a.session, a.modelProvider, tracker)
	if err != nil {
		return "", err
	}
	agentInst, err := buildADKAgent(ctx, a.cfg, tracker, tools, spec.instruction, spec.maxStep, a.modelProvider.EffectiveContextWindow(), summaryOpt)
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}
	logger.SetBatch(a.repo.NextManualBatchIndex(a.sessionID))
	// Shared notes are tagging-time working state: main-model runs get the
	// long-term context only (notes stay reachable through recall).
	msgs := append([]*schema.Message{SharedContextMessage(RenderSharedContext(spec.shared))}, spec.messages...)
	result, err := runADKWithRetry(ctx, a.cfg, agentInst, msgs, logger, spec.label, tracker)
	if err != nil {
		return "", err
	}
	_ = a.repo.RefreshSessionCounts(a.sessionID)
	if result == nil {
		return "", nil
	}
	return strings.TrimSpace(result.Content), nil
}

const (
	// chatHistoryBudget is the token budget of verbatim history; past it the
	// older turns are folded into a summary turn.
	chatHistoryBudget = 12000
	// chatKeepTurns is how many recent turns stay verbatim after a fold.
	chatKeepTurns = 6
	chatFilesMax  = 100
)

// Turn rows with these roles are never shown to the user and never part of
// ChatHistory: the epoch snapshot (a JSON SharedContext keyed by the summary
// turn that opened the epoch) and the exact user message that was sent for a
// user turn (keyed by that turn).
const (
	chatRoleSnapshot = "snapshot"
	chatRoleSent     = "sent"
)

// The output for a history is a prefix of the output for that history plus
// later turns, so provider prefix caches keep hitting. A user turn replays
// the exact message that was sent for it, not the text the user typed.
func chatHistoryMessages(summary *model.ChatTurn, turns []model.ChatTurn, sent map[uint]string) []*schema.Message {
	var msgs []*schema.Message
	if summary != nil {
		msgs = append(msgs, &schema.Message{Role: schema.User, Content: "## 之前对话的摘要\n\n" + summary.Content})
	}
	for _, t := range turns {
		role := schema.User
		if t.Role == db.ChatRoleAssistant {
			role = schema.Assistant
		}
		content := t.Content
		if s, ok := sent[t.ID]; ok && role == schema.User {
			content = s
		}
		msgs = append(msgs, &schema.Message{Role: role, Content: content})
	}
	return msgs
}

func (a *Agent) sentMessages(turns []model.ChatTurn) (map[uint]string, error) {
	var ids []uint
	for _, t := range turns {
		if t.Role == db.ChatRoleUser {
			ids = append(ids, t.ID)
		}
	}
	out := map[uint]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.ChatTurn
	if err := a.repo.DB().Where("session_id = ? AND role = ? AND covers_through IN ?", a.sessionID, chatRoleSent, ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.CoversThrough] = r.Content
	}
	return out, nil
}

// chatEpochSnapshot is the shared context the epoch opened with. It is stored
// so every turn of the epoch renders byte-identical message 0, however much
// the plan, categories or memory changed since.
func (a *Agent) chatEpochSnapshot(summary *model.ChatTurn) (SharedContext, error) {
	var epoch uint
	if summary != nil {
		epoch = summary.ID
	}
	var row model.ChatTurn
	err := a.repo.DB().Where("session_id = ? AND role = ? AND covers_through = ?", a.sessionID, chatRoleSnapshot, epoch).Order("id DESC").Take(&row).Error
	if err == nil {
		var c SharedContext
		if json.Unmarshal([]byte(row.Content), &c) == nil {
			return c, nil
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return SharedContext{}, err
	}
	c, err := LoadSharedContext(a.repo, a.session)
	if err != nil {
		return c, err
	}
	// Stored as the renderers see it: the plan is cut where the snapshot cuts
	// it, which keeps the row well under a 64 KB text column.
	stored := c
	if c.Plan != nil {
		p := *c.Plan
		p.Content = planText(c.Plan)
		stored.Plan = &p
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return c, err
	}
	if err := a.repo.AddChatTurn(&model.ChatTurn{SessionID: a.sessionID, Role: chatRoleSnapshot, Content: string(raw), CoversThrough: epoch}); err != nil {
		return c, err
	}
	return c, nil
}

func turnsTokens(turns []model.ChatTurn) int64 {
	var n int64
	for _, t := range turns {
		n += estimateTokens(t.Content)
	}
	return n
}

type chatSummarizer func(ctx context.Context, prev string, turns []model.ChatTurn) (string, error)

// Folding happens once past chatHistoryBudget, so later turns again share a
// stable prefix.
func compactChatHistory(ctx context.Context, repo *db.Repository, sessionID, runID uint, summary *model.ChatTurn, turns []model.ChatTurn, summarize chatSummarizer) (*model.ChatTurn, []model.ChatTurn, error) {
	if len(turns) <= chatKeepTurns || turnsTokens(turns) <= chatHistoryBudget {
		return summary, turns, nil
	}
	fold, keep := turns[:len(turns)-chatKeepTurns], turns[len(turns)-chatKeepTurns:]
	prev := ""
	if summary != nil {
		prev = summary.Content
	}
	text, err := summarize(ctx, prev, fold)
	if err != nil {
		return nil, nil, fmt.Errorf("summarize chat history: %w", err)
	}
	s := &model.ChatTurn{SessionID: sessionID, RunID: runID, Role: db.ChatRoleSummary, Content: strings.TrimSpace(text), CoversThrough: fold[len(fold)-1].ID}
	if err := repo.AddChatTurn(s); err != nil {
		return nil, nil, err
	}
	return s, keep, nil
}

func modelChatSummarizer(m einomodel.BaseChatModel) chatSummarizer {
	return func(ctx context.Context, prev string, turns []model.ChatTurn) (string, error) {
		var sb strings.Builder
		if prev != "" {
			sb.WriteString("## 已有摘要\n\n" + prev + "\n\n")
		}
		sb.WriteString("## 需要并入摘要的对话\n\n")
		for _, t := range turns {
			who := "用户"
			if t.Role == db.ChatRoleAssistant {
				who = "助手"
			}
			sb.WriteString(fmt.Sprintf("**%s**：%s\n\n", who, t.Content))
		}
		msg, err := m.Generate(ctx, []*schema.Message{
			{Role: schema.System, Content: chatSummaryPrompt},
			{Role: schema.User, Content: sb.String()},
		})
		if err != nil {
			return "", err
		}
		return msg.Content, nil
	}
}

func renderSelectedFiles(files []model.FileEntry) string {
	if len(files) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## 用户选中的文件（%d 项）\n\n", len(files)))
	for i, f := range files {
		if i == chatFilesMax {
			sb.WriteString(fmt.Sprintf("- …另有 %d 项\n", len(files)-chatFilesMax))
			break
		}
		kind := "文件"
		if f.FileType == "directory" {
			kind = "目录"
		}
		sb.WriteString(fmt.Sprintf("- %s（%s，%s）", SanitizeUntrustedName(f.OriginalPath), kind, humanSize(f.Size)))
		if f.NewPath != "" {
			sb.WriteString(" → " + SanitizeUntrustedName(f.NewPath))
		}
		if f.Description != "" {
			sb.WriteString("：" + SanitizeUntrustedName(oneLine(f.Description)))
		}
		sb.WriteString("\n")
	}
	return sb.String() + "\n"
}

func (a *Agent) RunChat(ctx context.Context, logger *Logger, content string, files []model.FileEntry) (string, error) {
	user := &model.ChatTurn{SessionID: a.sessionID, RunID: logger.RunID(), Role: db.ChatRoleUser, Content: content}
	if err := a.repo.AddChatTurn(user); err != nil {
		return "", err
	}
	logger.LogMessage("user", content)

	summary, turns, err := a.repo.ChatHistory(a.sessionID, user.ID)
	if err != nil {
		return "", err
	}
	summary, turns, err = compactChatHistory(ctx, a.repo, a.sessionID, logger.RunID(), summary, turns, a.chatSummarizer(logger))
	if err != nil {
		return "", err
	}

	base, err := a.chatEpochSnapshot(summary)
	if err != nil {
		return "", err
	}
	cur, err := LoadSharedContext(a.repo, a.session)
	if err != nil {
		return "", err
	}
	// The message sent is stored verbatim: the next turn replays it, so its
	// request starts with this request's messages.
	sent, err := a.sentMessages(turns)
	if err != nil {
		return "", err
	}
	delta := RenderDelta(base, cur, false)
	if delta == "" {
		for _, s := range sent {
			if strings.Contains(s, deltaHeading) {
				delta = deltaNone // an earlier message still lists changes that no longer hold
				break
			}
		}
	}
	sentText := delta + renderSelectedFiles(files) + content
	if err := a.repo.AddChatTurn(&model.ChatTurn{SessionID: a.sessionID, RunID: logger.RunID(), Role: chatRoleSent, Content: sentText, CoversThrough: user.ID}); err != nil {
		return "", err
	}
	msgs := chatHistoryMessages(summary, turns, sent)
	msgs = append(msgs, &schema.Message{Role: schema.User, Content: sentText})
	maxStep := a.cfg.ChatMaxStep
	answer, err := a.runMainAgent(ctx, logger, mainRunSpec{label: "chat", phase: db.RunPhaseChat, maxStep: maxStep, instruction: chatSystemPrompt(a.cfg), shared: base, current: cur.Categories, messages: msgs})
	if err != nil {
		return "", err
	}
	if answer == "" {
		answer = "（助手没有给出回复）"
	}
	if err := a.repo.AddChatTurn(&model.ChatTurn{SessionID: a.sessionID, RunID: logger.RunID(), Role: db.ChatRoleAssistant, Content: answer}); err != nil {
		return "", err
	}
	return answer, nil
}

func (a *Agent) chatSummarizer(logger *Logger) chatSummarizer {
	return func(ctx context.Context, prev string, turns []model.ChatTurn) (string, error) {
		p, err := modelfactory.ResolveBulkProvider(a.repo, a.session, a.modelProvider)
		if err != nil {
			return "", err
		}
		m, err := newChatModel(ctx, p)
		if err != nil {
			return "", err
		}
		tracker := newTokenTrackingModel(m)
		defer func() { logger.AddUsage(tracker.Usage()) }()
		return modelChatSummarizer(tracker)(ctx, prev, turns)
	}
}

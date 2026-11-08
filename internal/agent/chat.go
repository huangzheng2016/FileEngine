package agent

import (
	"context"
	"fmt"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"
	"FileEngine/internal/remotefs"
)

// newChatModel builds the model of a chat / survey / review run.
var newChatModel = modelfactory.NewChatModelFromProvider

// SetModelFactory replaces the model factory of chat, survey and review runs
// and returns a restore func. Tests use it to drive runs with fake models.
func SetModelFactory(f func(context.Context, *db.ModelProvider) (einomodel.ChatModel, error)) (restore func()) {
	old := newChatModel
	newChatModel = f
	return func() { newChatModel = old }
}

// mainRunSpec describes one main-model agent run (chat, survey, review).
type mainRunSpec struct {
	label       string
	maxStep     int
	instruction string // static system prompt of the run kind
	// messages follow the shared-context message: history, then this run's
	// own input last.
	messages []*schema.Message
}

// sharedContextMessage wraps the shared context block as the cached message
// that precedes everything run-specific.
func sharedContextMessage(block string) *schema.Message {
	if strings.TrimSpace(block) == "" {
		block = "（还没有目标、方案、记忆或分类）\n"
	}
	return modelfactory.MarkCacheBreakpoint(&schema.Message{Role: schema.User, Content: "# 会话共享上下文\n\n" + block})
}

// runMainAgent runs a main-model agent with the session assistant tool set
// over: system prompt → shared context → spec.messages. Returns the final
// assistant text.
func (a *Agent) runMainAgent(ctx context.Context, logger *Logger, spec mainRunSpec) (string, error) {
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
	tb := NewToolBuilder(a.repo, fs, a.sessionID, a.session.FilesystemID, logger, config.Get().Agent, a.session, a.modelProvider.VisionEnabled)
	tb.RunID = logger.RunID()
	tools, err := tb.BuildChatTools()
	if err != nil {
		return "", fmt.Errorf("build tools: %w", err)
	}
	summaryOpt, err := bulkSummaryOption(ctx, a.repo, a.session, a.modelProvider, tracker)
	if err != nil {
		return "", err
	}
	agentInst, err := buildADKAgent(ctx, tracker, tools, spec.instruction, spec.maxStep, a.modelProvider.EffectiveContextWindow(), summaryOpt)
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}
	// Shared notes are tagging-time working state: main-model runs get the
	// long-term context only (notes stay reachable through recall).
	shared, err := BuildSharedContext(a.repo, a.session)
	if err != nil {
		return "", err
	}
	logger.SetBatch(a.repo.NextManualBatchIndex(a.sessionID))
	msgs := append([]*schema.Message{sharedContextMessage(shared)}, spec.messages...)
	result, err := runADKWithRetry(ctx, agentInst, msgs, logger, spec.label, tracker)
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
	// chatFilesMax caps the selected files listed in one message.
	chatFilesMax = 100
)

// chatHistoryMessages renders persisted history as prompt messages. The
// output for a history is a prefix of the output for the same history plus
// later turns, so provider prefix caches hit turn after turn.
func chatHistoryMessages(summary *db.ChatTurn, turns []db.ChatTurn) []*schema.Message {
	var msgs []*schema.Message
	if summary != nil {
		msgs = append(msgs, &schema.Message{Role: schema.User, Content: "## 之前对话的摘要\n\n" + summary.Content})
	}
	for _, t := range turns {
		role := schema.User
		if t.Role == db.ChatRoleAssistant {
			role = schema.Assistant
		}
		msgs = append(msgs, &schema.Message{Role: role, Content: t.Content})
	}
	return msgs
}

func turnsTokens(turns []db.ChatTurn) int64 {
	var n int64
	for _, t := range turns {
		n += estimateTokens(t.Content)
	}
	return n
}

// chatSummarizer condenses the previous summary plus older turns into one
// summary text.
type chatSummarizer func(ctx context.Context, prev string, turns []db.ChatTurn) (string, error)

// compactChatHistory returns the history to send for a new turn. While the
// verbatim turns fit chatHistoryBudget nothing changes (append-only); past it
// all but the last chatKeepTurns are folded into a new persisted summary
// turn once, so the next turns again share a stable prefix.
func compactChatHistory(ctx context.Context, repo *db.Repository, sessionID, runID uint, summary *db.ChatTurn, turns []db.ChatTurn, summarize chatSummarizer) (*db.ChatTurn, []db.ChatTurn, error) {
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
	s := &db.ChatTurn{SessionID: sessionID, RunID: runID, Role: db.ChatRoleSummary, Content: strings.TrimSpace(text), CoversThrough: fold[len(fold)-1].ID}
	if err := repo.AddChatTurn(s); err != nil {
		return nil, nil, err
	}
	return s, keep, nil
}

// modelChatSummarizer summarizes with one plain model call.
func modelChatSummarizer(m einomodel.BaseChatModel) chatSummarizer {
	return func(ctx context.Context, prev string, turns []db.ChatTurn) (string, error) {
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

// renderSelectedFiles lists the files the user attached to a chat message.
func renderSelectedFiles(files []db.FileEntry) string {
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
		sb.WriteString(fmt.Sprintf("- %s（%s，%s）", f.OriginalPath, kind, humanSize(f.Size)))
		if f.NewPath != "" {
			sb.WriteString(" → " + f.NewPath)
		}
		if f.Description != "" {
			sb.WriteString("：" + oneLine(f.Description))
		}
		sb.WriteString("\n")
	}
	return sb.String() + "\n"
}

// RunChat answers one user turn of the session assistant. The user turn and
// the final answer are persisted as ChatTurns; the answer is returned as the
// run summary.
func (a *Agent) RunChat(ctx context.Context, logger *Logger, content string, files []db.FileEntry) (string, error) {
	user := &db.ChatTurn{SessionID: a.sessionID, RunID: logger.RunID(), Role: db.ChatRoleUser, Content: content}
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

	msgs := chatHistoryMessages(summary, turns)
	msgs = append(msgs, &schema.Message{Role: schema.User, Content: renderSelectedFiles(files) + content})
	maxStep := config.Get().Agent.ChatMaxStep
	answer, err := a.runMainAgent(ctx, logger, mainRunSpec{label: "chat", maxStep: maxStep, instruction: chatPrompt, messages: msgs})
	if err != nil {
		return "", err
	}
	if answer == "" {
		answer = "（助手没有给出回复）"
	}
	if err := a.repo.AddChatTurn(&db.ChatTurn{SessionID: a.sessionID, RunID: logger.RunID(), Role: db.ChatRoleAssistant, Content: answer}); err != nil {
		return "", err
	}
	return answer, nil
}

// chatSummarizer folds history with the session's bulk model, counted on
// the run's tokens.
func (a *Agent) chatSummarizer(logger *Logger) chatSummarizer {
	return func(ctx context.Context, prev string, turns []db.ChatTurn) (string, error) {
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

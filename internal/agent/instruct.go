package agent

import (
	"context"
	"fmt"
	"strings"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"
	"FileEngine/internal/remotefs"

	"github.com/cloudwego/eino/schema"
)

// InstructAgent handles user-directed operations.
type InstructAgent struct {
	repo          *db.Repository
	fsCfg         config.RemoteFSConfig
	modelProvider *db.ModelProvider
	session       *db.ScanSession
	sessionID     uint
	logger        *Logger
}

func NewInstructAgent(repo *db.Repository, fsCfg config.RemoteFSConfig, modelProvider *db.ModelProvider, session *db.ScanSession, sessionID uint, logger *Logger) *InstructAgent {
	return &InstructAgent{
		repo:          repo,
		fsCfg:         fsCfg,
		modelProvider: modelProvider,
		session:       session,
		sessionID:     sessionID,
		logger:        logger,
	}
}

func (a *InstructAgent) Run(ctx context.Context, files []db.FileEntry, userPrompt string) (string, error) {
	chatModel, err := modelfactory.NewChatModelFromProvider(ctx, a.modelProvider)
	if err != nil {
		return "", fmt.Errorf("create model: %w", err)
	}

	workerFS, err := remotefs.NewFromConfig(a.fsCfg)
	if err != nil {
		return "", fmt.Errorf("connect fs: %w", err)
	}
	defer workerFS.Close()

	filesystemID := a.session.FilesystemID
	tracker := newTokenTrackingModel(chatModel)
	toolBuilder := NewToolBuilder(a.repo, workerFS, a.sessionID, filesystemID, a.logger, config.Get().Agent, a.session)
	tools, err := toolBuilder.BuildInstructTools()
	if err != nil {
		return "", fmt.Errorf("build tools: %w", err)
	}

	instruction := DefaultInstructPrompt
	if p := config.Get().Agent.InstructSystemPrompt; p != "" {
		instruction = p
	}

	agentInst, err := buildADKAgent(ctx, tracker, tools, instruction, config.Get().Agent.InstructMaxStep, a.modelProvider.EffectiveContextWindow())
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}

	// Build message with file context
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## 用户指令\n\n%s\n\n", userPrompt))
	sb.WriteString("## 选中的文件/目录\n\n")
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("- %s (%s, %d bytes)", f.OriginalPath, f.FileType, f.Size))
		if f.Description != "" {
			sb.WriteString(fmt.Sprintf(" [描述: %s]", f.Description))
		}
		if f.NewPath != "" {
			sb.WriteString(fmt.Sprintf(" [目标: %s]", f.NewPath))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n请使用工具执行用户的指令。")

	a.logger.SetBatch(a.repo.NextManualBatchIndex(a.sessionID))
	userMsg := sb.String()
	a.logger.LogMessage("user", userMsg, 0, 0, 0)

	messages := []*schema.Message{
		{Role: schema.User, Content: userMsg},
	}

	label := "instruct"
	result, err := runADKWithRetry(ctx, agentInst, messages, a.logger, label, tracker)
	if err != nil {
		return "", err
	}

	response := ""
	if result != nil {
		pt, ct, tt := tracker.Usage()
		a.logger.LogMessage("assistant", result.Content, pt, ct, tt)
		response = result.Content
	}

	_ = a.repo.RefreshSessionCounts(a.sessionID)
	return response, nil
}

package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"
	"FileEngine/internal/remotefs"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

type Agent struct {
	repo          *db.Repository
	fsCfg         config.RemoteFSConfig
	cfg           *config.Config
	modelProvider *db.ModelProvider
	session       *db.ScanSession
	logger        *Logger
	sessionID     uint

	mu       sync.Mutex
	running  bool
	cancelFn context.CancelFunc
}

func New(repo *db.Repository, fsCfg config.RemoteFSConfig, cfg *config.Config, sessionID uint, modelProvider *db.ModelProvider, session *db.ScanSession) *Agent {
	return &Agent{
		repo:          repo,
		fsCfg:         fsCfg,
		cfg:           cfg,
		modelProvider: modelProvider,
		session:       session,
		sessionID:     sessionID,
		logger:        NewLogger(repo, sessionID),
	}
}

func (a *Agent) GetLogger() *Logger {
	return a.logger
}

func (a *Agent) IsRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

func (a *Agent) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelFn != nil {
		a.cancelFn()
	}
	a.running = false
}

func (a *Agent) RunTagging(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("agent is already running")
	}
	a.running = true
	ctx, cancel := context.WithCancel(ctx)
	a.cancelFn = cancel
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.running = false
		a.cancelFn = nil
		a.mu.Unlock()
	}()

	// Update session status
	session, err := a.repo.GetSession(a.sessionID)
	if err != nil {
		return err
	}
	session.Status = "tagging"
	if err := a.repo.UpdateSession(session); err != nil {
		return err
	}

	// Build LLM model (shared across workers, thread-safe)
	var chatModel einomodel.ChatModel
	chatModel, err = modelfactory.NewChatModelFromProvider(ctx, a.modelProvider)
	if err != nil {
		return fmt.Errorf("create model: %w", err)
	}

	// Get system prompt
	systemPrompt := DefaultSystemPrompt
	if a.cfg.Agent.SystemPrompt != "" {
		systemPrompt = a.cfg.Agent.SystemPrompt
	}

	// Get max depth
	maxDepth, err := a.repo.GetMaxDepth(a.sessionID)
	if err != nil {
		return fmt.Errorf("get max depth: %w", err)
	}

	concurrency := a.cfg.Agent.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	filesystemID := session.FilesystemID
	var batchCounter int32

	// Process each depth level from deepest to shallowest
	for depth := maxDepth; depth >= 0; depth-- {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		dirs, err := a.repo.GetUntaggedDirectoriesAtDepth(a.sessionID, depth)
		if err != nil {
			return fmt.Errorf("get dirs at depth %d: %w", depth, err)
		}
		if len(dirs) == 0 {
			continue
		}

		log.Printf("depth %d: %d untagged directories", depth, len(dirs))

		// Create work channel
		workCh := make(chan db.FileEntry, len(dirs))
		for _, dir := range dirs {
			workCh <- dir
		}
		close(workCh)

		// Launch workers
		var wg sync.WaitGroup
		workerCount := concurrency
		if workerCount > len(dirs) {
			workerCount = len(dirs)
		}

		for w := 0; w < workerCount; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				a.runWorker(ctx, workerID, workCh, chatModel, systemPrompt, &batchCounter, filesystemID)
			}(w)
		}

		wg.Wait()

		// Refresh session counts after each depth level
		_ = a.repo.RefreshSessionCounts(a.sessionID)
	}

	// Process remaining untagged files (leaf files not in any directory)
	a.processRemainingFiles(ctx, chatModel, systemPrompt, &batchCounter, filesystemID)

	// Update session status
	session, err = a.repo.GetSession(a.sessionID)
	if err != nil {
		return err
	}
	session.Status = "tagged"
	return a.repo.UpdateSession(session)
}

// RunInstruct executes a single agent call with user-selected files and a custom prompt.
// Returns the assistant's response content.
func (a *Agent) RunInstruct(ctx context.Context, files []db.FileEntry, userPrompt string) (string, error) {
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
	toolBuilder := NewToolBuilder(a.repo, workerFS, a.sessionID, filesystemID, a.logger, a.cfg.Agent, a.session)
	tools, err := toolBuilder.BuildInstructTools()
	if err != nil {
		return "", fmt.Errorf("build tools: %w", err)
	}

	agentInst, err := react.NewAgent(ctx, &react.AgentConfig{
		Model:       chatModel,
		ToolsConfig: compose.ToolsNodeConfig{Tools: tools},
		MaxStep:     30,
	})
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}

	// Build message with file context
	var sb strings.Builder
	sb.WriteString("用户选择了以下文件/目录，请根据用户指令进行操作：\n\n")
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
	sb.WriteString(fmt.Sprintf("\n用户指令: %s\n", userPrompt))
	sb.WriteString("\n请使用工具执行用户的指令。可以修改描述(update_description)、设置目标路径(set_target)、查看分类(list_categories)、创建分类(create_category)。")

	a.logger.SetBatch(-1)
	userMsg := sb.String()
	a.logger.LogMessage("user", userMsg, 0, 0, 0)

	systemPrompt := "你是一个文件整理助手。用户选择了一些文件并给出了指令，请使用工具完成用户的要求。"

	messages := []*schema.Message{
		{Role: schema.System, Content: systemPrompt},
		{Role: schema.User, Content: userMsg},
	}

	result, err := agentInst.Generate(ctx, messages)
	if err != nil {
		a.logger.LogMessage("system", fmt.Sprintf("Error: %v", err), 0, 0, 0)
		return "", err
	}

	response := ""
	if result != nil {
		pt, ct, tt := extractTokenUsage(result)
		a.logger.LogMessage("assistant", result.Content, pt, ct, tt)
		response = result.Content
	}

	_ = a.repo.RefreshSessionCounts(a.sessionID)
	return response, nil
}

func (a *Agent) runWorker(ctx context.Context, workerID int, workCh <-chan db.FileEntry, chatModel einomodel.ChatModel, systemPrompt string, batchCounter *int32, filesystemID uint) {
	// Each worker gets its own FS connection
	workerFS, err := remotefs.NewFromConfig(a.fsCfg)
	if err != nil {
		log.Printf("worker %d: failed to connect fs: %v", workerID, err)
		return
	}
	defer workerFS.Close()

	// Each worker gets its own logger and agent
	workerLogger := NewLogger(a.repo, a.sessionID)
	toolBuilder := NewToolBuilder(a.repo, workerFS, a.sessionID, filesystemID, workerLogger, a.cfg.Agent, a.session)
	tools, err := toolBuilder.BuildTools()
	if err != nil {
		log.Printf("worker %d: failed to build tools: %v", workerID, err)
		return
	}

	// Create ReAct agent for this worker
	agentInst, err := react.NewAgent(ctx, &react.AgentConfig{
		Model: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: tools,
		},
		MaxStep: 50,
	})
	if err != nil {
		log.Printf("worker %d: failed to create agent: %v", workerID, err)
		return
	}

	for dir := range workCh {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Re-check: may have been cascade-tagged by another worker
		tagged, err := a.repo.IsFileTagged(a.sessionID, dir.OriginalPath)
		if err != nil || tagged {
			continue
		}

		batchIdx := atomic.AddInt32(batchCounter, 1)
		workerLogger.SetBatch(int(batchIdx))

		userMsg := buildDirectoryPrompt(dir)
		workerLogger.LogMessage("user", userMsg, 0, 0, 0)

		messages := []*schema.Message{
			{Role: schema.System, Content: systemPrompt},
			{Role: schema.User, Content: userMsg},
		}

		result, err := agentInst.Generate(ctx, messages)
		if err != nil {
			log.Printf("worker %d batch %d error: %v", workerID, batchIdx, err)
			workerLogger.LogMessage("system", fmt.Sprintf("Error: %v", err), 0, 0, 0)
			continue
		}

		if result != nil {
			pt, ct, tt := extractTokenUsage(result)
			workerLogger.LogMessage("assistant", result.Content, pt, ct, tt)
		}
	}
}

func (a *Agent) processRemainingFiles(ctx context.Context, chatModel einomodel.ChatModel, systemPrompt string, batchCounter *int32, filesystemID uint) {
	falseVal := false
	files, _, err := a.repo.ListFiles(db.FileQuery{
		SessionID: a.sessionID,
		FileType:  "file",
		Tagged:    &falseVal,
		Page:      1,
		PageSize:  a.cfg.Agent.BatchSize,
	})
	if err != nil || len(files) == 0 {
		return
	}

	// Process remaining files in a single worker
	workerFS, err := remotefs.NewFromConfig(a.fsCfg)
	if err != nil {
		return
	}
	defer workerFS.Close()

	workerLogger := NewLogger(a.repo, a.sessionID)
	toolBuilder := NewToolBuilder(a.repo, workerFS, a.sessionID, filesystemID, workerLogger, a.cfg.Agent, a.session)
	tools, err := toolBuilder.BuildTools()
	if err != nil {
		return
	}

	agentInst, err := react.NewAgent(ctx, &react.AgentConfig{
		Model: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: tools,
		},
		MaxStep: 50,
	})
	if err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		falseVal := false
		files, _, err := a.repo.ListFiles(db.FileQuery{
			SessionID: a.sessionID,
			FileType:  "file",
			Tagged:    &falseVal,
			Page:      1,
			PageSize:  a.cfg.Agent.BatchSize,
		})
		if err != nil || len(files) == 0 {
			return
		}

		batchIdx := atomic.AddInt32(batchCounter, 1)
		workerLogger.SetBatch(int(batchIdx))

		userMsg := buildBatchPrompt(files)
		workerLogger.LogMessage("user", userMsg, 0, 0, 0)

		messages := []*schema.Message{
			{Role: schema.System, Content: systemPrompt},
			{Role: schema.User, Content: userMsg},
		}

		result, err := agentInst.Generate(ctx, messages)
		if err != nil {
			log.Printf("remaining files batch %d error: %v", batchIdx, err)
			continue
		}
		if result != nil {
			pt, ct, tt := extractTokenUsage(result)
			workerLogger.LogMessage("assistant", result.Content, pt, ct, tt)
		}
		_ = a.repo.RefreshSessionCounts(a.sessionID)
	}
}

// extractTokenUsage extracts prompt/completion/total tokens from a message's ResponseMeta.
func extractTokenUsage(msg *schema.Message) (prompt, completion, total int) {
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return 0, 0, 0
	}
	u := msg.ResponseMeta.Usage
	return u.PromptTokens, u.CompletionTokens, u.TotalTokens
}

func buildDirectoryPrompt(dir db.FileEntry) string {
	var sb strings.Builder
	sb.WriteString("分析以下目录：\n\n")
	sb.WriteString(fmt.Sprintf("路径: %s\n", dir.OriginalPath))
	sb.WriteString(fmt.Sprintf("类型: %s | 大小: %d | 子项数: %d | 深度: %d\n", dir.FileType, dir.Size, dir.ChildCount, dir.Depth))
	if dir.Description != "" {
		sb.WriteString(fmt.Sprintf("当前描述: %s\n", dir.Description))
	}
	if dir.ParentPath != "" {
		sb.WriteString(fmt.Sprintf("父目录: %s\n", dir.ParentPath))
	}
	sb.WriteString("\n操作步骤：\n")
	sb.WriteString(fmt.Sprintf("1. 使用 list_files(parent_path=\"%s\") 查看目录内容\n", dir.OriginalPath))
	sb.WriteString("2. 读取关键文件（README、配置文件等）了解用途\n")
	sb.WriteString("3. **重要**：如果路径很深或目录名看起来是某个大项目的子目录（如 node_modules、src、lib、spec、test 等），\n")
	sb.WriteString("   请使用 get_file_info 向上逐级查看父目录，找到项目根目录（通常包含 README、package.json、go.mod 等）\n")
	sb.WriteString("   然后对项目根目录执行 update_description + set_target + mark_tagged，一次性标记整个项目\n")
	sb.WriteString("4. 使用 update_description 写描述\n")
	sb.WriteString("5. 如果是完整单元（项目、安装包、相册等）：\n")
	sb.WriteString("   → 使用 list_categories 查找目标分类\n")
	sb.WriteString("   → 使用 set_target 设置目标路径\n")
	sb.WriteString("   → 使用 mark_tagged 标记（所有子项自动标记）\n")
	sb.WriteString("6. 如果内容混杂，仅描述，不要 mark_tagged\n")
	return sb.String()
}

func buildBatchPrompt(entries []db.FileEntry) string {
	var sb strings.Builder
	sb.WriteString("Please analyze and organize the following files:\n\n")
	for i, e := range entries {
		sb.WriteString(fmt.Sprintf("%d. Path: %s\n   Type: %s | Size: %d\n",
			i+1, e.OriginalPath, e.FileType, e.Size))
		if e.Description != "" {
			sb.WriteString(fmt.Sprintf("   Current description: %s\n", e.Description))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("For each item:\n")
	sb.WriteString("1. Read the file if needed to understand it\n")
	sb.WriteString("2. Write a description using update_description\n")
	sb.WriteString("3. Plan_move it to the right category and mark_tagged\n")
	return sb.String()
}

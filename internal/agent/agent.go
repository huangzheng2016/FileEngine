package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

	// Get max depth
	maxDepth, err := a.repo.GetMaxDepth(a.sessionID)
	if err != nil {
		return fmt.Errorf("get max depth: %w", err)
	}

	filesystemID := session.FilesystemID
	var batchCounter int32 = int32(a.repo.MaxBatchIndex(a.sessionID))

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

		// Read live config each depth level
		liveCfg := config.Get()
		concurrency := liveCfg.Agent.Concurrency
		if concurrency <= 0 {
			concurrency = 1
		}
		systemPrompt := DefaultSystemPrompt
		if liveCfg.Agent.SystemPrompt != "" {
			systemPrompt = liveCfg.Agent.SystemPrompt
		}

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
	remainingPrompt := DefaultSystemPrompt
	if p := config.Get().Agent.SystemPrompt; p != "" {
		remainingPrompt = p
	}
	a.processRemainingFiles(ctx, chatModel, remainingPrompt, &batchCounter, filesystemID)

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
	tracker := newTokenTrackingModel(chatModel)
	toolBuilder := NewToolBuilder(a.repo, workerFS, a.sessionID, filesystemID, a.logger, config.Get().Agent, a.session)
	tools, err := toolBuilder.BuildInstructTools()
	if err != nil {
		return "", fmt.Errorf("build tools: %w", err)
	}

	agentInst, err := react.NewAgent(ctx, &react.AgentConfig{
		Model:       tracker,
		ToolsConfig: compose.ToolsNodeConfig{Tools: tools},
		MaxStep:     30,
	})
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

	systemPrompt := `你是一个文件整理助手。用户选择了一些文件并给出了指令，请使用工具完成用户的要求。

## 重要：主动理解用户意图

- 用户说"拆开"、"分开"、"不要合集"等：先用 list_files 探索子目录，然后对每个子目录分别 set_target
- 用户说"合并"、"放到一起"等：用 list_categories 查看所有分类，找到相关的，合并后删除多余分类
- 用户说"重新分类"、"换个分类"等：先清除旧 target，再设置新的
- 用户说"改名"、"改路径"、"改描述"等：直接用对应工具修改
- 操作前先用 list_categories 了解现有分类

## 可用工具

- list_files: 列出目录内容（探索子目录时必用）
- get_file_info: 获取文件/目录详细信息
- update_description: 修改描述
- mark_tagged: 标记目录为已处理（级联子项）
- set_target: 设置目标路径（传空字符串清除已有目标）
- list_categories: 查看所有分类
- list_category_files: 查看分类下已规划的文件
- update_category: 修改分类（路径变更会级联更新文件）
- delete_category: 删除分类（清除该分类下所有文件的规划，允许重新分类）
- create_category: 创建新分类（如果可用）

## 常见操作流程

### 拆分目录
用户要求将大目录拆分为子目录分别归类：
1. set_target(大目录, "") 清除整体目标
2. list_files 查看子目录/文件
3. 对每个子项根据内容 set_target 到合适的分类路径
4. mark_tagged 标记已处理的目录

### 合并分类
将多个分类合并为一个：
1. list_categories 查看所有分类
2. update_category 将目标分类改为期望的名称和路径
3. 对源分类：update_category 改路径到目标（文件自动级联），然后 delete_category

### 重新分类
将文件从一个分类移到另一个：
1. list_categories 查看可用分类
2. set_target 设置新的目标路径

### 批量改描述
用户要求统一修改描述格式（如"都改成 YYYYMMDD+内容 格式"）：
1. 对每个选中的文件/目录调用 update_description

### 批量改目标路径
用户要求统一调整路径格式（如"路径都加上年份前缀"）：
1. list_categories 确认分类路径
2. 对每个文件 set_target 设置新路径

### 取消规划
用户要求撤销某些文件的分类规划：
1. set_target(文件, "") 清除目标路径

### 深入探索后分类
用户选了一个大目录但想按内容细分：
1. list_files 逐层探索目录结构
2. get_file_info 了解关键文件/目录
3. 根据内容对不同子目录 set_target 到不同分类
4. mark_tagged 标记已处理`

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
		pt, ct, tt := tracker.Usage()
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

	// Each worker gets its own logger, token tracker, and agent
	workerLogger := NewLogger(a.repo, a.sessionID)
	tracker := newTokenTrackingModel(chatModel)
	toolBuilder := NewToolBuilder(a.repo, workerFS, a.sessionID, filesystemID, workerLogger, config.Get().Agent, a.session)
	tools, err := toolBuilder.BuildTools()
	if err != nil {
		log.Printf("worker %d: failed to build tools: %v", workerID, err)
		return
	}

	// Create ReAct agent for this worker
	agentInst, err := react.NewAgent(ctx, &react.AgentConfig{
		Model: tracker,
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

		result, err := func() (*schema.Message, error) {
			callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
			defer callCancel()
			return agentInst.Generate(callCtx, messages)
		}()
		if err != nil {
			// Retry on failure
			maxRetries := config.Get().Agent.MaxRetries
			if maxRetries <= 0 {
				maxRetries = 3
			}
			for retry := 1; retry <= maxRetries && err != nil; retry++ {
				if ctx.Err() != nil {
					return // parent context cancelled, stop immediately
				}
				log.Printf("worker %d batch %d retry %d/%d: %v", workerID, batchIdx, retry, maxRetries, err)
				time.Sleep(time.Duration(retry) * 2 * time.Second)
				callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
				result, err = agentInst.Generate(callCtx, messages)
				callCancel()
			}
			if err != nil {
				log.Printf("worker %d batch %d error after %d retries: %v", workerID, batchIdx, maxRetries, err)
				workerLogger.LogMessage("system", fmt.Sprintf("Error after %d retries: %v", maxRetries, err), 0, 0, 0)
				continue
			}
		}

		if result != nil {
			pt, ct, tt := tracker.Usage()
			workerLogger.LogMessage("assistant", result.Content, pt, ct, tt)
			tracker.ResetUsage()
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
		PageSize:  config.Get().Agent.BatchSize,
	})
	if err != nil || len(files) == 0 {
		return
	}

	// Process remaining files in a single worker
	workerFS, err := remotefs.NewFromConfig(a.fsCfg)
	if err != nil {
		log.Printf("processRemainingFiles: failed to connect fs: %v", err)
		return
	}
	defer workerFS.Close()

	workerLogger := NewLogger(a.repo, a.sessionID)
	tracker := newTokenTrackingModel(chatModel)
	toolBuilder := NewToolBuilder(a.repo, workerFS, a.sessionID, filesystemID, workerLogger, config.Get().Agent, a.session)
	tools, err := toolBuilder.BuildTools()
	if err != nil {
		return
	}

	agentInst, err := react.NewAgent(ctx, &react.AgentConfig{
		Model: tracker,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: tools,
		},
		MaxStep: 50,
	})
	if err != nil {
		return
	}

	for iterations := 0; ; iterations++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Safety limit: prevent infinite loop if agent fails to mark files
		if iterations > 1000 {
			log.Printf("processRemainingFiles: exceeded max iterations, stopping")
			return
		}

		falseVal := false
		files, _, err := a.repo.ListFiles(db.FileQuery{
			SessionID: a.sessionID,
			FileType:  "file",
			Tagged:    &falseVal,
			Page:      1,
			PageSize:  config.Get().Agent.BatchSize,
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

		result, err := func() (*schema.Message, error) {
			callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
			defer callCancel()
			return agentInst.Generate(callCtx, messages)
		}()
		if err != nil {
			maxRetries := config.Get().Agent.MaxRetries
			if maxRetries <= 0 {
				maxRetries = 3
			}
			for retry := 1; retry <= maxRetries && err != nil; retry++ {
				if ctx.Err() != nil {
					return
				}
				log.Printf("remaining files batch %d retry %d/%d: %v", batchIdx, retry, maxRetries, err)
				time.Sleep(time.Duration(retry) * 2 * time.Second)
				callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
				result, err = agentInst.Generate(callCtx, messages)
				callCancel()
			}
			if err != nil {
				log.Printf("remaining files batch %d error after %d retries: %v", batchIdx, maxRetries, err)
				workerLogger.LogMessage("system", fmt.Sprintf("Error after %d retries: %v", maxRetries, err), 0, 0, 0)
				continue
			}
		}
		if result != nil {
			pt, ct, tt := tracker.Usage()
			workerLogger.LogMessage("assistant", result.Content, pt, ct, tt)
			tracker.ResetUsage()
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
	sb.WriteString("处理以下目录：\n\n")
	sb.WriteString(fmt.Sprintf("路径: %s\n", dir.OriginalPath))
	sb.WriteString(fmt.Sprintf("类型: %s | 大小: %d | 子项数: %d | 深度: %d\n", dir.FileType, dir.Size, dir.ChildCount, dir.Depth))
	if dir.Description != "" {
		sb.WriteString(fmt.Sprintf("当前描述: %s\n", dir.Description))
	}
	if dir.ParentPath != "" {
		sb.WriteString(fmt.Sprintf("父目录: %s\n", dir.ParentPath))
	}
	return sb.String()
}

func buildBatchPrompt(entries []db.FileEntry) string {
	var sb strings.Builder
	sb.WriteString("处理以下散文件：\n\n")
	for i, e := range entries {
		sb.WriteString(fmt.Sprintf("%d. 路径: %s\n   类型: %s | 大小: %d\n",
			i+1, e.OriginalPath, e.FileType, e.Size))
		if e.Description != "" {
			sb.WriteString(fmt.Sprintf("   描述: %s\n", e.Description))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

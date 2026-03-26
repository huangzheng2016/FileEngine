package agent

import (
	"context"
	"fmt"
	"log"
	"sort"
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

type fileBatch struct {
	files    []db.FileEntry
	batchIdx int32
}

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
		// If stopped (context cancelled), update status to "tagged" instead of leaving "tagging"
		if ctx.Err() != nil {
			if s, err := a.repo.GetSession(a.sessionID); err == nil && s.Status == "tagging" {
				s.Status = "tagged"
				a.repo.UpdateSession(s)
			}
		}
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

		// Distribute directories by interleaving different parent paths
		// so concurrent workers process different subtrees instead of the same one
		buckets := make(map[string][]db.FileEntry)
		var bucketKeys []string
		for _, dir := range dirs {
			key := dir.ParentPath
			if _, exists := buckets[key]; !exists {
				bucketKeys = append(bucketKeys, key)
			}
			buckets[key] = append(buckets[key], dir)
		}
		sort.Strings(bucketKeys)

		workCh := make(chan db.FileEntry, len(dirs))
		for more := true; more; {
			more = false
			for _, key := range bucketKeys {
				if len(buckets[key]) > 0 {
					workCh <- buckets[key][0]
					buckets[key] = buckets[key][1:]
					more = true
				}
			}
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
		MaxStep:     config.Get().Agent.InstructMaxStep,
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

	messages := []*schema.Message{
		{Role: schema.System, Content: DefaultInstructPrompt},
		{Role: schema.User, Content: userMsg},
	}

	result, err := generateWithRetry(ctx, agentInst, messages, a.logger, "instruct")
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
	defer workerLogger.Flush()
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
		MaxStep: config.Get().Agent.MaxStep,
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

		label := fmt.Sprintf("worker %d batch %d", workerID, batchIdx)
		result, err := generateWithRetry(ctx, agentInst, messages, workerLogger, label)
		if err != nil {
			continue
		}

		if result != nil {
			pt, ct, tt := tracker.Usage()
			workerLogger.LogMessage("assistant", result.Content, pt, ct, tt)
			tracker.ResetUsage()
		}
		workerLogger.Flush()
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

	liveCfg := config.Get()
	concurrency := liveCfg.Agent.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	workCh := make(chan fileBatch, concurrency)

	// Dispatcher: fetch batches and send to workers
	go func() {
		defer close(workCh)
		for iterations := 0; ; iterations++ {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if iterations > 1000 {
				log.Printf("processRemainingFiles: exceeded max iterations, stopping")
				return
			}
			falseVal := false
			batch, _, err := a.repo.ListFiles(db.FileQuery{
				SessionID: a.sessionID,
				FileType:  "file",
				Tagged:    &falseVal,
				Page:      1,
				PageSize:  config.Get().Agent.BatchSize,
			})
			if err != nil || len(batch) == 0 {
				return
			}
			batchIdx := atomic.AddInt32(batchCounter, 1)
			select {
			case workCh <- fileBatch{files: batch, batchIdx: batchIdx}:
			case <-ctx.Done():
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			a.runRemainingFilesWorker(ctx, workerID, workCh, chatModel, systemPrompt, filesystemID)
		}(w)
	}
	wg.Wait()
	_ = a.repo.RefreshSessionCounts(a.sessionID)
}

func (a *Agent) runRemainingFilesWorker(ctx context.Context, workerID int, workCh <-chan fileBatch, chatModel einomodel.ChatModel, systemPrompt string, filesystemID uint) {
	workerFS, err := remotefs.NewFromConfig(a.fsCfg)
	if err != nil {
		log.Printf("remaining worker %d: failed to connect fs: %v", workerID, err)
		return
	}
	defer workerFS.Close()

	workerLogger := NewLogger(a.repo, a.sessionID)
	defer workerLogger.Flush()
	tracker := newTokenTrackingModel(chatModel)
	toolBuilder := NewToolBuilder(a.repo, workerFS, a.sessionID, filesystemID, workerLogger, config.Get().Agent, a.session)
	tools, err := toolBuilder.BuildTools()
	if err != nil {
		log.Printf("remaining worker %d: failed to build tools: %v", workerID, err)
		return
	}

	agentInst, err := react.NewAgent(ctx, &react.AgentConfig{
		Model: tracker,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: tools,
		},
		MaxStep: config.Get().Agent.MaxStep,
	})
	if err != nil {
		log.Printf("remaining worker %d: failed to create agent: %v", workerID, err)
		return
	}

	for batch := range workCh {
		select {
		case <-ctx.Done():
			return
		default:
		}

		workerLogger.SetBatch(int(batch.batchIdx))
		userMsg := buildBatchPrompt(batch.files)
		workerLogger.LogMessage("user", userMsg, 0, 0, 0)

		messages := []*schema.Message{
			{Role: schema.System, Content: systemPrompt},
			{Role: schema.User, Content: userMsg},
		}

		label := fmt.Sprintf("remaining worker %d batch %d", workerID, batch.batchIdx)
		result, err := generateWithRetry(ctx, agentInst, messages, workerLogger, label)
		if err != nil {
			continue
		}
		if result != nil {
			pt, ct, tt := tracker.Usage()
			workerLogger.LogMessage("assistant", result.Content, pt, ct, tt)
			tracker.ResetUsage()
		}
		workerLogger.Flush()
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

func generateWithRetry(ctx context.Context, agentInst *react.Agent, messages []*schema.Message, logger *Logger, label string) (*schema.Message, error) {
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()
	result, err := agentInst.Generate(callCtx, messages)
	if err == nil {
		return result, nil
	}

	maxRetries := config.Get().Agent.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	for retry := 1; retry <= maxRetries; retry++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Printf("%s retry %d/%d: %v", label, retry, maxRetries, err)
		time.Sleep(time.Duration(retry) * 2 * time.Second)
		retryCtx, retryCancel := context.WithTimeout(ctx, 60*time.Second)
		result, err = agentInst.Generate(retryCtx, messages)
		retryCancel()
		if err == nil {
			return result, nil
		}
	}
	logger.LogMessage("system", fmt.Sprintf("Error after %d retries: %v", maxRetries, err), 0, 0, 0)
	return nil, err
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

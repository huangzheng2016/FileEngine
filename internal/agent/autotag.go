package agent

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"
	"FileEngine/internal/remotefs"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// AutoTagAgent handles automatic file classification.
type AutoTagAgent struct {
	repo          *db.Repository
	fsCfg         config.RemoteFSConfig
	modelProvider *db.ModelProvider
	session       *db.ScanSession
	sessionID     uint
}

func NewAutoTagAgent(repo *db.Repository, fsCfg config.RemoteFSConfig, modelProvider *db.ModelProvider, session *db.ScanSession, sessionID uint) *AutoTagAgent {
	return &AutoTagAgent{
		repo:          repo,
		fsCfg:         fsCfg,
		modelProvider: modelProvider,
		session:       session,
		sessionID:     sessionID,
	}
}

func (a *AutoTagAgent) Run(ctx context.Context) error {
	// Build LLM model (shared across workers, thread-safe)
	chatModel, err := modelfactory.NewChatModelFromProvider(ctx, a.modelProvider)
	if err != nil {
		return fmt.Errorf("create model: %w", err)
	}

	// Get max depth
	maxDepth, err := a.repo.GetMaxDepth(a.sessionID)
	if err != nil {
		return fmt.Errorf("get max depth: %w", err)
	}

	filesystemID := a.session.FilesystemID
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

	return nil
}

func (a *AutoTagAgent) runWorker(ctx context.Context, workerID int, workCh <-chan db.FileEntry, chatModel einomodel.ChatModel, systemPrompt string, batchCounter *int32, filesystemID uint) {
	workerFS, err := remotefs.NewFromConfig(a.fsCfg)
	if err != nil {
		log.Printf("worker %d: failed to connect fs: %v", workerID, err)
		return
	}
	defer workerFS.Close()

	workerLogger := NewLogger(a.repo, a.sessionID)
	defer workerLogger.Flush()
	tracker := newTokenTrackingModel(chatModel)
	toolBuilder := NewToolBuilder(a.repo, workerFS, a.sessionID, filesystemID, workerLogger, config.Get().Agent, a.session)
	tools, err := toolBuilder.BuildTools()
	if err != nil {
		log.Printf("worker %d: failed to build tools: %v", workerID, err)
		return
	}

	agentInst, err := buildADKAgent(ctx, tracker, tools, systemPrompt, config.Get().Agent.MaxStep, a.modelProvider.EffectiveContextWindow())
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

		batchIdx := atomic.AddInt32(batchCounter, 1)
		workerLogger.SetBatch(int(batchIdx))

		// Atomic claim: prevent TOCTOU race where another worker processes the same directory
		claimed, err := a.repo.ClaimDirectory(a.sessionID, dir.OriginalPath, int(batchIdx))
		if err != nil || !claimed {
			continue
		}

		usages, _ := a.repo.GetCategoryUsageSummary(filesystemID, a.sessionID)
		catSummary := buildCategorySummary(usages)
		userMsg := buildDirectoryPrompt(dir, catSummary)
		workerLogger.LogMessage("user", userMsg, 0, 0, 0)

		messages := []*schema.Message{
			{Role: schema.User, Content: userMsg},
		}

		label := fmt.Sprintf("worker %d batch %d", workerID, batchIdx)
		result, err := runADKWithRetry(ctx, agentInst, messages, workerLogger, label, tracker)
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

func (a *AutoTagAgent) processRemainingFiles(ctx context.Context, chatModel einomodel.ChatModel, systemPrompt string, batchCounter *int32, filesystemID uint) {
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

	// Dispatcher: fetch batches with cursor-based pagination and send to workers
	go func() {
		defer close(workCh)
		var lastMaxID uint
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
				MinID:     lastMaxID,
			})
			if err != nil || len(batch) == 0 {
				return
			}
			// Advance cursor to the largest ID in this batch
			for _, f := range batch {
				if f.ID > lastMaxID {
					lastMaxID = f.ID
				}
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

func (a *AutoTagAgent) runRemainingFilesWorker(ctx context.Context, workerID int, workCh <-chan fileBatch, chatModel einomodel.ChatModel, systemPrompt string, filesystemID uint) {
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

	agentInst, err := buildADKAgent(ctx, tracker, tools, systemPrompt, config.Get().Agent.MaxStep, a.modelProvider.EffectiveContextWindow())
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
		usages, _ := a.repo.GetCategoryUsageSummary(filesystemID, a.sessionID)
		catSummary := buildCategorySummary(usages)
		userMsg := buildBatchPrompt(batch.files, catSummary)
		workerLogger.LogMessage("user", userMsg, 0, 0, 0)

		messages := []*schema.Message{
			{Role: schema.User, Content: userMsg},
		}

		label := fmt.Sprintf("remaining worker %d batch %d", workerID, batch.batchIdx)
		result, err := runADKWithRetry(ctx, agentInst, messages, workerLogger, label, tracker)
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

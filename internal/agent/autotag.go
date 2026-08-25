package agent

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"
	"FileEngine/internal/remotefs"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// BatchFailure records one batch whose agent run failed after all retries.
type BatchFailure struct {
	BatchIndex int
	Target     string // directory path or "N files"
	Err        string
}

// AutoTagAgent handles automatic file classification.
type AutoTagAgent struct {
	repo          *db.Repository
	fsCfg         config.RemoteFSConfig
	modelProvider *db.ModelProvider
	session       *db.ScanSession
	sessionID     uint

	mu       sync.Mutex
	failures []BatchFailure
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

// noteFailure records a failed batch (called from concurrent workers).
func (a *AutoTagAgent) noteFailure(f BatchFailure) {
	a.mu.Lock()
	a.failures = append(a.failures, f)
	a.mu.Unlock()
}

// Failures returns a copy of the batch failures collected during the run.
func (a *AutoTagAgent) Failures() []BatchFailure {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]BatchFailure, len(a.failures))
	copy(out, a.failures)
	return out
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
		systemPrompt := systemPromptForSession(liveCfg.Agent, a.session)

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

	// Process remaining untagged files (leaf files not in any directory), then
	// re-sweep with a fresh cursor: a batch can succeed while leaving files
	// untagged (set_target does not flip tagged), and the dispatcher's ID
	// cursor never looks back, so only new passes from ID 0 reach such misses.
	// Directories are deliberately NOT re-swept — container dirs stay untagged
	// by design and re-running them would re-tag containers forever.
	remainingPrompt := systemPromptForSession(config.Get().Agent, a.session)
	runRemainingSweeps(maxRemainingSweeps, func() {
		a.processRemainingFiles(ctx, chatModel, remainingPrompt, &batchCounter, filesystemID)
	}, func() (int64, error) {
		b, err := a.repo.CountUntaggedByType(a.sessionID)
		return b.Files, err
	})

	return nil
}

// maxRemainingSweeps bounds the extra end-of-run sweeps over loose files
// (beyond the first pass).
const maxRemainingSweeps = 2

// runRemainingSweeps runs process once, then repeats it up to maxExtra times
// while countRemaining keeps reporting progress (a strictly decreasing
// remainder). A round that moves nothing stops the loop — an agent that keeps
// missing the same files would otherwise be retried forever. Count errors
// stop the sweeps too; the first pass has already run at that point.
func runRemainingSweeps(maxExtra int, process func(), countRemaining func() (int64, error)) {
	process()
	prev, err := countRemaining()
	if err != nil {
		log.Printf("remaining sweep: count failed, skipping re-sweeps: %v", err)
		return
	}
	for i := 0; i < maxExtra && prev > 0; i++ {
		process()
		cur, err := countRemaining()
		if err != nil {
			log.Printf("remaining sweep: count failed, stopping sweeps: %v", err)
			return
		}
		if cur >= prev {
			return
		}
		prev = cur
	}
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
	toolBuilder := NewToolBuilder(a.repo, workerFS, a.sessionID, filesystemID, workerLogger, config.Get().Agent, a.session, a.modelProvider.VisionEnabled)
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

		workerLogger.EmitBatchStart(dir.OriginalPath)

		usages, err := a.repo.GetCategoryUsageSummary(filesystemID, a.sessionID)
		if err != nil {
			log.Printf("worker %d: category usage summary failed, prompt will lack it: %v", workerID, err)
		} else {
			// The agent is about to see this list in the prompt — snapshot it
			// for the create_category version guard.
			toolBuilder.noteCategoryUsages(usages)
		}
		catSummary := buildCategorySummary(usages)
		userMsg := buildDirectoryPrompt(dir, catSummary)
		workerLogger.LogMessage("user", userMsg, 0, 0, 0)

		messages := []*schema.Message{
			{Role: schema.User, Content: userMsg},
		}

		label := fmt.Sprintf("worker %d batch %d", workerID, batchIdx)
		result, err := runADKWithRetry(ctx, agentInst, messages, workerLogger, label, tracker)
		if err != nil {
			log.Printf("%s (%s) failed after retries: %v", label, dir.OriginalPath, err)
			a.noteFailure(BatchFailure{BatchIndex: int(batchIdx), Target: dir.OriginalPath, Err: err.Error()})
			a.emitTagProgress(int(batchIdx))
			continue
		}

		if result != nil {
			pt, ct, tt := tracker.Usage()
			workerLogger.LogMessage("assistant", result.Content, pt, ct, tt)
			tracker.ResetUsage()
		}
		workerLogger.Flush()
		a.emitTagProgress(int(batchIdx))
	}
}

// emitTagProgress publishes absolute tagging counters after a finished batch.
func (a *AutoTagAgent) emitTagProgress(batchIdx int) {
	p, err := a.repo.GetSessionProgress(a.sessionID)
	if err != nil {
		return
	}
	db.PublishEvent(db.Event{Type: db.EventProgress, SessionID: a.sessionID, Payload: db.ProgressPayload{
		SessionID:  a.sessionID,
		Phase:      db.PhaseTagging,
		Total:      p.Total,
		Tagged:     p.Tagged,
		Planned:    p.Planned,
		BatchIndex: batchIdx,
	}})
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
	if err != nil {
		log.Printf("processRemainingFiles: initial list files failed: %v", err)
		return
	}
	if len(files) == 0 {
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
			if err != nil {
				log.Printf("processRemainingFiles: list files failed, stopping dispatch: %v", err)
				return
			}
			if len(batch) == 0 {
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
	toolBuilder := NewToolBuilder(a.repo, workerFS, a.sessionID, filesystemID, workerLogger, config.Get().Agent, a.session, a.modelProvider.VisionEnabled)
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
		workerLogger.EmitBatchStart(fmt.Sprintf("%d files", len(batch.files)))
		usages, err := a.repo.GetCategoryUsageSummary(filesystemID, a.sessionID)
		if err != nil {
			log.Printf("remaining worker %d: category usage summary failed, prompt will lack it: %v", workerID, err)
		} else {
			// The agent is about to see this list in the prompt — snapshot it
			// for the create_category version guard.
			toolBuilder.noteCategoryUsages(usages)
		}
		catSummary := buildCategorySummary(usages)
		userMsg := buildBatchPrompt(batch.files, catSummary)
		workerLogger.LogMessage("user", userMsg, 0, 0, 0)

		messages := []*schema.Message{
			{Role: schema.User, Content: userMsg},
		}

		label := fmt.Sprintf("remaining worker %d batch %d", workerID, batch.batchIdx)
		result, err := runADKWithRetry(ctx, agentInst, messages, workerLogger, label, tracker)
		if err != nil {
			target := fmt.Sprintf("%d files", len(batch.files))
			log.Printf("%s (%s) failed after retries: %v", label, target, err)
			a.noteFailure(BatchFailure{BatchIndex: int(batch.batchIdx), Target: target, Err: err.Error()})
			a.emitTagProgress(int(batch.batchIdx))
			continue
		}
		if result != nil {
			pt, ct, tt := tracker.Usage()
			workerLogger.LogMessage("assistant", result.Content, pt, ct, tt)
			tracker.ResetUsage()
		}
		// Batch-level re-check: a successful run does not guarantee every file
		// got mark_tagged — verify against the manifest and record misses.
		a.recheckBatchTagged(int(batch.batchIdx), batch.files)
		workerLogger.Flush()
		a.emitTagProgress(int(batch.batchIdx))
	}
}

// recheckBatchTagged verifies that a finished loose-file batch actually
// tagged every file it was given. set_target alone does not flip tagged, so
// an agent can return success while leaving files behind; until now such
// misses were silent (the dispatcher cursor had already passed their IDs).
// Misses are recorded as batch failures so they surface exactly like hard
// failures (tagged_partial status + report section), and the end-of-run
// sweeps give them another pass.
func (a *AutoTagAgent) recheckBatchTagged(batchIdx int, files []db.FileEntry) {
	ids := make([]uint, len(files))
	paths := make(map[uint]string, len(files))
	for i, f := range files {
		ids[i] = f.ID
		paths[f.ID] = f.OriginalPath
	}
	tagged, err := a.repo.TaggedStatusByIDs(ids)
	if err != nil {
		log.Printf("batch %d: tagged re-check failed (misses stay silent): %v", batchIdx, err)
		return
	}
	var missed []string
	for _, id := range ids {
		if !tagged[id] {
			missed = append(missed, paths[id])
		}
	}
	if len(missed) == 0 {
		return
	}
	const maxListed = 5
	shown := missed
	suffix := ""
	if len(missed) > maxListed {
		shown = missed[:maxListed]
		suffix = fmt.Sprintf(" 等 %d 个", len(missed))
	}
	a.noteFailure(BatchFailure{
		BatchIndex: batchIdx,
		Target:     fmt.Sprintf("%d files", len(files)),
		Err: fmt.Sprintf("agent 完成但未标记 %d/%d 个文件: %s%s",
			len(missed), len(files), strings.Join(shown, ", "), suffix),
	})
}

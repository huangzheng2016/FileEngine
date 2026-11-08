package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"
	"FileEngine/internal/remotefs"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type BatchFailure struct {
	BatchIndex int
	Target     string
	Err        string
}

type AutoTagAgent struct {
	repo          *db.Repository
	fsCfg         config.RemoteFSConfig
	modelProvider *db.ModelProvider
	session       *db.ScanSession
	sessionID     uint
	logger        *Logger

	mu       sync.Mutex
	failures []BatchFailure
}

func NewAutoTagAgent(repo *db.Repository, fsCfg config.RemoteFSConfig, modelProvider *db.ModelProvider, session *db.ScanSession, sessionID uint, logger *Logger) *AutoTagAgent {
	return &AutoTagAgent{repo: repo, fsCfg: fsCfg, modelProvider: modelProvider, session: session, sessionID: sessionID, logger: logger}
}

func (a *AutoTagAgent) noteFailure(f BatchFailure) {
	a.mu.Lock()
	a.failures = append(a.failures, f)
	a.mu.Unlock()
}

func (a *AutoTagAgent) Failures() []BatchFailure {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]BatchFailure(nil), a.failures...)
}

const (
	unitDir      = "dir"      // decide one directory: unit / container / skip
	unitGroup    = "group"    // the loose undecided files of one container
	unitVersions = "versions" // one version group of siblings
)

type workUnit struct {
	kind     string
	path     string // directory (dir) or parent directory (group, versions)
	files    []db.FileEntry
	versions VersionGroup
}

func (u workUnit) label() string {
	switch u.kind {
	case unitGroup:
		return fmt.Sprintf("%d 个文件 · %s", len(u.files), displayPath(u.path))
	case unitVersions:
		return "版本组 " + u.versions.Name
	}
	return displayPath(u.path)
}

// pending counts pushed-but-unfinished units, so workers stop only when all
// work is done.
type unitQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	items   []workUnit
	pending int
	stopped bool
}

func newUnitQueue() *unitQueue {
	q := &unitQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *unitQueue) push(units ...workUnit) {
	q.mu.Lock()
	q.items = append(q.items, units...)
	q.pending += len(units)
	q.mu.Unlock()
	q.cond.Broadcast()
}

func (q *unitQueue) pop() (workUnit, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && q.pending > 0 && !q.stopped {
		q.cond.Wait()
	}
	if len(q.items) == 0 || q.stopped {
		return workUnit{}, false
	}
	u := q.items[0]
	q.items = q.items[1:]
	return u, true
}

func (q *unitQueue) done() {
	q.mu.Lock()
	q.pending--
	q.mu.Unlock()
	q.cond.Broadcast()
}

func (q *unitQueue) stop() {
	q.mu.Lock()
	q.stopped = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

type unitRunner func(ctx context.Context, w *tagWorker, u workUnit) error

func (a *AutoTagAgent) Run(ctx context.Context) error {
	bulk, err := modelfactory.ResolveBulkProvider(a.repo, a.session, a.modelProvider)
	if err != nil {
		return err
	}
	a.modelProvider = bulk
	chatModel, err := modelfactory.NewChatModelFromProvider(ctx, a.modelProvider)
	if err != nil {
		return fmt.Errorf("create model: %w", err)
	}
	if err := a.prepass(); err != nil {
		return err
	}
	return a.walk(ctx, func(workerID int) (*tagWorker, func(), error) {
		return a.newWorker(ctx, workerID, chatModel)
	})
}

func (a *AutoTagAgent) prepass() error {
	if !config.Get().Agent.JunkFilterDisabled {
		if n, err := a.repo.MarkJunkEntries(a.sessionID, junkFileNames, junkDirNames); err != nil {
			log.Printf("junk filter: %v (continuing)", err)
		} else if n > 0 {
			log.Printf("junk filter: skipped %d system junk entries", n)
		}
	}
	if err := a.repo.PrepareSession(a.sessionID); err != nil {
		return fmt.Errorf("prepare session: %w", err)
	}
	a.emitTagProgress()
	return nil
}

func (a *AutoTagAgent) walk(ctx context.Context, newWorker func(workerID int) (*tagWorker, func(), error)) error {
	q := newUnitQueue()
	root, err := a.expandContainer("")
	if err != nil {
		return err
	}
	if len(root) == 0 {
		return nil
	}
	q.push(root...)
	go func() {
		<-ctx.Done()
		q.stop()
	}()

	concurrency := max(config.Get().Agent.Concurrency, 1)
	var wg sync.WaitGroup
	for id := range concurrency {
		w, closeFn, err := newWorker(id)
		if err != nil {
			log.Printf("worker %d: %v", id, err)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer closeFn()
			for {
				u, ok := q.pop()
				if !ok {
					return
				}
				children, err := a.processUnit(ctx, w, u)
				if err != nil {
					log.Printf("unit %s: %v", u.label(), err)
				}
				q.push(children...)
				q.done()
			}
		}()
	}
	wg.Wait()
	_ = a.repo.RefreshSessionCounts(a.sessionID)
	return ctx.Err()
}

func (a *AutoTagAgent) expandContainer(dir string) ([]workUnit, error) {
	children, err := a.repo.ListChildren(a.sessionID, dir, 0)
	if err != nil {
		return nil, err
	}
	var units []workUnit
	inGroup := map[string]bool{}
	for _, g := range DetectVersionGroups(children) {
		units = append(units, workUnit{kind: unitVersions, path: dir, versions: g})
		for _, m := range g.Members {
			inGroup[m.Path] = true
		}
	}
	var loose []db.FileEntry
	for _, c := range children {
		if inGroup[c.OriginalPath] {
			continue
		}
		switch {
		case c.FileType == "directory" && (c.Role == db.RoleUndecided || c.Role == db.RoleContainer):
			units = append(units, workUnit{kind: unitDir, path: c.OriginalPath})
		case c.FileType == "file" && c.Role == db.RoleUndecided:
			loose = append(loose, c)
		}
	}
	if len(loose) > 0 {
		units = append(units, workUnit{kind: unitGroup, path: dir, files: loose})
	}
	return units, nil
}

func (a *AutoTagAgent) processUnit(ctx context.Context, w *tagWorker, u workUnit) ([]workUnit, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	switch u.kind {
	case unitDir:
		return a.processDir(ctx, w, u)
	default:
		w.logger.EmitPullBegin(u.label(), len(u.files)+len(u.versions.Members))
		ids := unitEntryIDs(a.repo, a.sessionID, u)
		err := w.run(ctx, w, u)
		missed := a.countUndecided(ids)
		if err != nil {
			a.noteFailure(BatchFailure{Target: u.label(), Err: err.Error()})
		} else if missed > 0 {
			a.noteFailure(BatchFailure{Target: u.label(), Err: fmt.Sprintf("完成但遗漏 %d/%d 个条目未决定", missed, len(ids))})
		}
		w.logger.EmitPullDone(len(ids)-missed, missed)
		a.emitTagProgress()
		return nil, err
	}
}

func unitEntryIDs(repo *db.Repository, sessionID uint, u workUnit) []uint {
	var ids []uint
	for _, f := range u.files {
		ids = append(ids, f.ID)
	}
	for _, m := range u.versions.Members {
		if e, err := repo.GetFileByPath(sessionID, m.Path); err == nil {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

func (a *AutoTagAgent) countUndecided(ids []uint) int {
	if len(ids) == 0 {
		return 0
	}
	tagged, err := a.repo.TaggedStatusByIDs(ids)
	if err != nil {
		return 0
	}
	n := 0
	for _, id := range ids {
		if !tagged[id] {
			n++
		}
	}
	return n
}

func (a *AutoTagAgent) processDir(ctx context.Context, w *tagWorker, u workUnit) ([]workUnit, error) {
	sig, entry, _, err := LoadDirSignals(a.repo, a.sessionID, u.path)
	if err != nil {
		return nil, err
	}
	switch entry.Role {
	case db.RoleContainer:
		return a.expandContainer(u.path)
	case db.RoleUndecided:
	default:
		return nil, nil
	}

	if done, err := a.autoDecide(entry, sig); err != nil {
		return nil, err
	} else if done {
		return a.afterDecision(u.path)
	}

	w.logger.EmitPullBegin(u.label(), 0)
	runErr := w.run(ctx, w, u)
	e, err := a.repo.GetFileByPath(a.sessionID, u.path)
	if err != nil {
		return nil, err
	}
	if e.Role == db.RoleUndecided {
		msg := "目录未被决定，按 container 继续处理子项"
		if runErr != nil {
			msg = runErr.Error() + "；" + msg
		}
		a.noteFailure(BatchFailure{Target: u.label(), Err: msg})
		if _, err := a.repo.Decide(a.sessionID, db.Decision{Path: u.path, Role: db.RoleContainer, Reason: "未能判断，逐项处理"}); err != nil && !db.IsRejection(err) {
			return nil, err
		}
		w.logger.EmitPullDone(0, 1)
	} else {
		w.logger.EmitPullDone(1, 0)
	}
	a.emitTagProgress()
	return a.afterDecision(u.path)
}

func (a *AutoTagAgent) afterDecision(dir string) ([]workUnit, error) {
	e, err := a.repo.GetFileByPath(a.sessionID, dir)
	if err != nil || e.Role != db.RoleContainer {
		return nil, err
	}
	return a.expandContainer(dir)
}

var (
	codeCategoryWords   = []string{"代码", "项目", "源码", "开发", "code", "project", "dev", "src"}
	bundleCategoryWords = []string{"软件", "应用", "程序", "app", "software"}
)

func matchCategory(cats []db.Category, words []string) *db.Category {
	for _, w := range words {
		for i := range cats {
			hay := strings.ToLower(cats[i].Name + " " + cats[i].Path + " " + cats[i].Description)
			if strings.Contains(hay, w) {
				return &cats[i]
			}
		}
	}
	return nil
}

func (a *AutoTagAgent) autoDecide(dir *db.FileEntry, sig DirSignals) (bool, error) {
	var undecided int64
	if err := a.repo.DB().Model(&db.FileEntry{}).
		Where("scan_session_id = ? AND parent_path = ? AND role IN ?", a.sessionID, dir.OriginalPath, []string{db.RoleUndecided, db.RoleContainer}).
		Count(&undecided).Error; err != nil {
		return false, err
	}
	if undecided == 0 {
		_, err := a.repo.Decide(a.sessionID, db.Decision{Path: dir.OriginalPath, Role: db.RoleContainer, Reason: "子项都已决定"})
		return err == nil, ignoreRejection(err)
	}
	m := sig.strongMarker()
	if m == nil {
		return false, nil
	}
	hasPlan, err := a.repo.HasPlan(a.sessionID)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(a.session.Goal) == "" && !hasPlan {
		return false, nil
	}
	cats, err := a.repo.ListCategories(a.session.FilesystemID)
	if err != nil {
		return false, err
	}
	words := codeCategoryWords
	if m.Kind == markerBundle {
		words = bundleCategoryWords
	}
	cat := matchCategory(cats, words)
	if cat == nil {
		return false, nil
	}
	_, err = a.repo.Decide(a.sessionID, db.Decision{
		Path: dir.OriginalPath, Role: db.RoleUnit,
		NewPath: strings.TrimSuffix(db.NormalizeTargetPath(cat.Path), "/") + "/" + dir.Name,
		Reason:  fmt.Sprintf("自动：检测到 %s（%s），整体归入「%s」", m.Name, m.Label, cat.Name),
	})
	return err == nil, ignoreRejection(err)
}

func ignoreRejection(err error) error {
	if db.IsRejection(err) {
		return nil
	}
	return err
}

type tagWorker struct {
	id      int
	logger  *Logger
	tb      *ToolBuilder
	agent   *adk.ChatModelAgent
	tracker *tokenTrackingModel
	fs      remotefs.RemoteFS
	run     unitRunner
}

func (a *AutoTagAgent) newWorker(ctx context.Context, id int, chatModel einomodel.ChatModel) (*tagWorker, func(), error) {
	fs, err := remotefs.NewFromConfig(a.fsCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("connect fs: %w", err)
	}
	cfg := config.Get().Agent
	logger := a.logger.ForWorker(id)
	tb := NewToolBuilder(a.repo, fs, a.sessionID, a.session.FilesystemID, logger, cfg, a.session, a.modelProvider.VisionEnabled)
	tb.RunID = logger.RunID()
	tools, err := tb.BuildTools()
	if err != nil {
		fs.Close()
		return nil, nil, fmt.Errorf("build tools: %w", err)
	}
	tracker := newTokenTrackingModel(chatModel)
	agentInst, err := buildADKAgent(ctx, tracker, tools, systemPromptForSession(cfg, a.session), cfg.MaxStep, a.modelProvider.EffectiveContextWindow(), withWorkerReduction())
	if err != nil {
		fs.Close()
		return nil, nil, fmt.Errorf("create agent: %w", err)
	}
	w := &tagWorker{id: id, logger: logger, tb: tb, agent: agentInst, tracker: tracker, fs: fs}
	w.run = a.runUnitLLM
	return w, func() { fs.Close() }, nil
}

// The shared-context message is byte-identical across the run's units, so it
// is a cache breakpoint.
func (a *AutoTagAgent) unitMessages(ctx context.Context, w *tagWorker, u workUnit) ([]*schema.Message, error) {
	shared, err := a.sharedContextFor(w.tb)
	if err != nil {
		return nil, err
	}
	notes, err := w.tb.TakeNewNotes()
	if err != nil {
		return nil, err
	}
	w.tb.SetUnitRoot(u.path)
	uc, err := LoadUnitContext(a.repo, a.session, []string{u.path}, notes)
	if err != nil {
		return nil, err
	}
	unitCtx := RenderUnitContext(uc)
	var msg string
	switch u.kind {
	case unitDir:
		sig, entry, children, err := LoadDirSignals(a.repo, a.sessionID, u.path)
		if err != nil {
			return nil, err
		}
		msg = buildDirUnitPrompt(entry, children, sig, unitCtx)
	case unitGroup:
		var hints map[uint]string
		if w.fs != nil {
			shown := u.files[:min(len(u.files), unitListingMax)]
			hints = a.archiveHints(ctx, w.fs, shown, config.Get().Agent)
		}
		msg = buildGroupUnitPrompt(u.path, u.files, unitCtx, hints)
	case unitVersions:
		msg = buildVersionUnitPrompt(u.path, u.versions, a.softwareTarget(u.versions.Name), unitCtx)
	}
	return []*schema.Message{SharedContextMessage(shared), schema.UserMessage(msg)}, nil
}

func (a *AutoTagAgent) sharedContextFor(tb *ToolBuilder) (string, error) {
	c, err := LoadSharedContext(a.repo, a.session)
	if err != nil {
		return "", err
	}
	names := make([]string, len(c.Categories))
	for i, cat := range c.Categories {
		names[i] = cat.Name
	}
	tb.guard.note(names)
	return RenderSharedContext(c), nil
}

func (a *AutoTagAgent) softwareTarget(name string) string {
	base := "/软件"
	if cats, err := a.repo.ListCategories(a.session.FilesystemID); err == nil {
		if c := matchCategory(cats, bundleCategoryWords); c != nil {
			base = db.NormalizeTargetPath(c.Path)
		}
	}
	return base + "/" + strings.ReplaceAll(name, "/", "_")
}

func (a *AutoTagAgent) runUnitLLM(ctx context.Context, w *tagWorker, u workUnit) error {
	msgs, err := a.unitMessages(ctx, w, u)
	if err != nil {
		return err
	}
	w.logger.LogMessage("user", msgs[1].Content)
	_, err = runADKWithRetry(ctx, w.agent, msgs, w.logger, fmt.Sprintf("worker %d %s", w.id, u.label()), w.tracker)
	return err
}

func (a *AutoTagAgent) emitTagProgress() {
	p, err := a.repo.GetSessionProgress(a.sessionID)
	if err != nil {
		return
	}
	a.logger.EmitProgress(db.ProgressData{Phase: db.RunPhaseTagging, Total: p.Total, Tagged: p.Tagged, Planned: p.Planned})
}

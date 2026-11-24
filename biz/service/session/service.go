// Package session owns the organizing tasks: creating one (with its template
// rules and categories) and starting its scan, editing its settings, rescanning,
// deleting it with everything that belongs to it, and the read models of the
// session list and detail.
package session

import (
	"context"
	"errors"
	"sort"
	"strings"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/errs"
	"FileEngine/biz/service/run"
	"FileEngine/internal/db"
	"FileEngine/internal/pathutil"

	"gorm.io/gen/field"
	"gorm.io/gorm"
)

const (
	maxDrafts = 200
	idChunk   = 500
)

var (
	errNoFilesystem = errs.Invalid("存储不存在")
	errPickModel    = errs.Invalid("请选择模型")
	errFilterMode   = errs.Invalid("过滤模式只能是 blacklist 或 whitelist")
	errTooMany      = errs.Invalid("规则和分类各不超过 200 条")
	errBusy         = errs.Conflict(db.ErrSessionBusy.Error())
)

type Service struct {
	q    *query.Query
	repo *db.Repository
	run  *run.Service
}

func NewService(q *query.Query, repo *db.Repository, r *run.Service) *Service {
	return &Service{q: q, repo: repo, run: r}
}

// View is a session with the read models the API shows next to it.
type View struct {
	*model.ScanSession
	LatestRun *db.RunRef
	// only set by Get
	Tokens *db.TokenCounts
}

type RuleDraft struct {
	Name, Exts, NameRegex, PathPrefix, FileType, Target, Role, Source string
	MinSize, MaxSize                                                  int64
	Position                                                          int
}

type CategoryDraft struct {
	Name, Path, Structure, Description string
}

type CreateInput struct {
	FilesystemID        uint
	ScanPath            string
	ModelProviderID     uint
	BulkModelProviderID uint
	ExcludeCategoryDirs bool
	FilterMode          string
	FilterDirs          string
	AllowVision         bool
	// nil = the default of a new task
	ExtractMetadata   *bool
	DetectDuplicates  bool
	Goal              string
	AutoTag           bool
	ScopeExts         string
	AllowReadFile     *bool
	AllowAutoCategory *bool
	AllowScript       *bool
	Rules             []RuleDraft
	Categories        []CategoryDraft
}

// Create stores the session with its template rules and categories in one
// transaction, then starts the scan.
func (s *Service) Create(ctx context.Context, in CreateInput) (*View, error) {
	fs := s.q.Filesystem
	filesystem, err := fs.WithContext(ctx).Where(fs.ID.Eq(in.FilesystemID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNoFilesystem
	}
	if err != nil {
		return nil, err
	}
	if err := s.modelExists(ctx, in.ModelProviderID, false); err != nil {
		return nil, err
	}
	if err := s.modelExists(ctx, in.BulkModelProviderID, true); err != nil {
		return nil, err
	}
	filterMode, err := normalizeFilterMode(in.FilterMode)
	if err != nil {
		return nil, err
	}
	if err := pathutil.ValidateFSPath(in.ScanPath); err != nil {
		return nil, errs.Invalid("扫描路径不合法：" + err.Error())
	}
	rules, err := validateRules(in.Rules)
	if err != nil {
		return nil, err
	}
	cats, err := validateCategories(in.Categories)
	if err != nil {
		return nil, err
	}

	root := run.ScanRoot(in.ScanPath)
	// Read from the input: gorm writes the column default back into the struct
	// of a false bool it omitted from the INSERT.
	want := struct{ readFile, autoCategory, script, metadata bool }{
		orTrue(in.AllowReadFile), orTrue(in.AllowAutoCategory), orTrue(in.AllowScript), orTrue(in.ExtractMetadata),
	}
	sess := &model.ScanSession{
		FilesystemID: filesystem.ID, ScanPath: root, RootPath: root, Protocol: filesystem.Protocol,
		Status: "scanning", AllowReadFile: want.readFile, AllowAutoCategory: want.autoCategory,
		AllowScript: want.script, ExtractMetadata: want.metadata,
		AllowVision: in.AllowVision, DetectDuplicates: in.DetectDuplicates, ExcludeCategoryDirs: in.ExcludeCategoryDirs,
		FilterMode: filterMode, FilterDirs: in.FilterDirs, Goal: strings.TrimSpace(in.Goal),
		ScopeExts: normalizeExts(in.ScopeExts), ModelProviderID: in.ModelProviderID, BulkModelProviderID: in.BulkModelProviderID,
	}
	err = s.q.Transaction(func(tx *query.Query) error {
		ss := tx.ScanSession
		if err := ss.WithContext(ctx).Create(sess); err != nil {
			return err
		}
		// A false bool is omitted from the INSERT, so the column default (on)
		// would win for the four switches that default to on.
		var off []field.AssignExpr
		if !want.readFile {
			off = append(off, ss.AllowReadFile.Value(false))
		}
		if !want.autoCategory {
			off = append(off, ss.AllowAutoCategory.Value(false))
		}
		if !want.script {
			off = append(off, ss.AllowScript.Value(false))
		}
		if !want.metadata {
			off = append(off, ss.ExtractMetadata.Value(false))
		}
		if len(off) > 0 {
			if _, err := ss.WithContext(ctx).Where(ss.ID.Eq(sess.ID)).UpdateSimple(off...); err != nil {
				return err
			}
		}
		return s.createTemplates(ctx, tx, sess, rules, cats)
	})
	if err != nil {
		return nil, err
	}
	sess.AllowReadFile, sess.AllowAutoCategory, sess.AllowScript, sess.ExtractMetadata =
		want.readFile, want.autoCategory, want.script, want.metadata
	if err := s.run.StartScan(ctx, sess, in.AutoTag); err != nil {
		_ = s.repo.FailScan(sess.ID, err.Error())
		return nil, err
	}
	return s.view(ctx, sess)
}

func orTrue(p *bool) bool { return p == nil || *p }

func (s *Service) createTemplates(ctx context.Context, tx *query.Query, sess *model.ScanSession, rules []*model.PlanRule, cats []*model.Category) error {
	pos := 0
	for _, r := range rules {
		pos = max(pos, r.Position)
	}
	for _, r := range rules {
		if r.Position <= 0 {
			pos++
			r.Position = pos
		}
		r.SessionID = sess.ID
		if err := tx.PlanRule.WithContext(ctx).Create(r); err != nil {
			return err
		}
	}
	if len(cats) == 0 {
		return nil
	}
	c := tx.Category
	existing, err := c.WithContext(ctx).Where(c.FilesystemID.Eq(sess.FilesystemID)).Find()
	if err != nil {
		return err
	}
	names, paths := map[string]bool{}, map[string]bool{}
	for _, e := range existing {
		names[e.Name], paths[e.Path] = true, true
	}
	for _, cat := range cats {
		if names[cat.Name] || paths[cat.Path] {
			continue
		}
		names[cat.Name], paths[cat.Path] = true, true
		cat.FilesystemID = sess.FilesystemID
		if err := c.WithContext(ctx).Create(cat); err != nil {
			return err
		}
	}
	return nil
}

func validateRules(drafts []RuleDraft) ([]*model.PlanRule, error) {
	if len(drafts) > maxDrafts {
		return nil, errTooMany
	}
	seen := map[string]bool{}
	out := make([]*model.PlanRule, len(drafts))
	for i, d := range drafts {
		r := &model.PlanRule{
			Name: d.Name, Exts: d.Exts, NameRegex: d.NameRegex, PathPrefix: d.PathPrefix, FileType: d.FileType,
			MinSize: d.MinSize, MaxSize: d.MaxSize, Target: d.Target, Role: d.Role, Position: d.Position, Source: d.Source,
		}
		if err := db.NormalizeRule(r); err != nil {
			return nil, ruleError(err)
		}
		switch r.Source {
		case "":
			r.Source = db.MemorySourceUser
		case "user", "agent", "preset":
		default:
			return nil, errs.Invalid("规则来源只能是 user、agent 或 preset")
		}
		if seen[r.Name] {
			return nil, errs.Invalid("已有同名规则「" + r.Name + "」")
		}
		seen[r.Name] = true
		out[i] = r
	}
	return out, nil
}

func ruleError(err error) error {
	if db.IsRejection(err) {
		return errs.Invalid(err.Error())
	}
	return err
}

func validateCategories(drafts []CategoryDraft) ([]*model.Category, error) {
	if len(drafts) > maxDrafts {
		return nil, errTooMany
	}
	out := make([]*model.Category, len(drafts))
	for i, d := range drafts {
		name := strings.TrimSpace(d.Name)
		if name == "" || strings.TrimSpace(d.Path) == "" {
			return nil, errs.Invalid("分类需要名称和路径")
		}
		if err := pathutil.ValidateFSPath(d.Path); err != nil {
			return nil, errs.Invalid("分类「" + name + "」的路径不合法：" + err.Error())
		}
		out[i] = &model.Category{Name: name, Path: d.Path, Structure: d.Structure, Description: d.Description}
	}
	return out, nil
}

func normalizeFilterMode(m string) (string, error) {
	switch m {
	case "":
		return "blacklist", nil
	case "blacklist", "whitelist":
		return m, nil
	}
	return "", errFilterMode
}

// normalizeExts stores the extensions the way rules do: lower-case, without
// dot, sorted, comma-separated.
func normalizeExts(s string) string {
	exts := make([]string, 0)
	for e := range db.ParseExts(s) {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	return strings.Join(exts, ",")
}

func (s *Service) modelExists(ctx context.Context, id uint, optional bool) error {
	if id == 0 {
		if optional {
			return nil
		}
		return errPickModel
	}
	m := s.q.ModelProvider
	n, err := m.WithContext(ctx).Where(m.ID.Eq(id)).Count()
	if err != nil {
		return err
	}
	if n == 0 {
		return errPickModel
	}
	return nil
}

// List returns the sessions of one storage (0 = every storage), newest first,
// each with its latest run.
func (s *Service) List(ctx context.Context, filesystemID uint) ([]*View, error) {
	ss := s.q.ScanSession
	do := ss.WithContext(ctx)
	if filesystemID != 0 {
		do = do.Where(ss.FilesystemID.Eq(filesystemID))
	}
	rows, err := do.Order(ss.ID.Desc()).Find()
	if err != nil {
		return nil, err
	}
	ids := make([]uint, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	latest, err := s.latestRuns(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]*View, len(rows))
	for i, r := range rows {
		out[i] = &View{ScanSession: r}
		if ref, ok := latest[r.ID]; ok {
			out[i].LatestRun = &ref
		}
	}
	return out, nil
}

func (s *Service) latestRuns(ctx context.Context, sessionIDs []uint) (map[uint]db.RunRef, error) {
	ar := s.q.AgentRun
	out := make(map[uint]db.RunRef, len(sessionIDs))
	for start := 0; start < len(sessionIDs); start += idChunk {
		chunk := sessionIDs[start:min(start+idChunk, len(sessionIDs))]
		var newest []struct{ ID uint }
		if err := ar.WithContext(ctx).Select(ar.ID.Max().As("id")).Where(ar.SessionID.In(chunk...)).Group(ar.SessionID).Scan(&newest); err != nil {
			return nil, err
		}
		runIDs := make([]uint, len(newest))
		for i, n := range newest {
			runIDs[i] = n.ID
		}
		runs, err := ar.WithContext(ctx).Select(ar.ID, ar.SessionID, ar.Phase, ar.Status).Where(ar.ID.In(runIDs...)).Find()
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			out[r.SessionID] = db.RunRef{ID: r.ID, Phase: r.Phase, Status: r.Status}
		}
	}
	return out, nil
}

func (s *Service) tokens(ctx context.Context, sessionID uint) (db.TokenCounts, error) {
	ar := s.q.AgentRun
	var t struct{ Prompt, Cached, CacheWrite, Completion, Total int }
	err := ar.WithContext(ctx).Select(
		ar.PromptTokens.Sum().As("prompt"), ar.CachedTokens.Sum().As("cached"),
		ar.CacheWriteTokens.Sum().As("cache_write"), ar.CompletionTokens.Sum().As("completion"),
		ar.TotalTokens.Sum().As("total"),
	).Where(ar.SessionID.Eq(sessionID)).Scan(&t)
	return db.TokenCounts(t), err
}

func (s *Service) load(ctx context.Context, id uint) (*model.ScanSession, error) {
	return s.run.Session(ctx, id)
}

func (s *Service) view(ctx context.Context, sess *model.ScanSession) (*View, error) {
	latest, err := s.latestRuns(ctx, []uint{sess.ID})
	if err != nil {
		return nil, err
	}
	v := &View{ScanSession: sess}
	if ref, ok := latest[sess.ID]; ok {
		v.LatestRun = &ref
	}
	return v, nil
}

// Get returns one session with its latest run and token totals.
func (s *Service) Get(ctx context.Context, id uint) (*View, error) {
	sess, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	v, err := s.view(ctx, sess)
	if err != nil {
		return nil, err
	}
	t, err := s.tokens(ctx, id)
	if err != nil {
		return nil, err
	}
	v.Tokens = &t
	return v, nil
}

// UpdateInput holds the settings to change; nil fields stay as they are.
type UpdateInput struct {
	AllowVision         *bool
	AllowReadFile       *bool
	AllowAutoCategory   *bool
	AllowScript         *bool
	ExcludeCategoryDirs *bool
	FilterMode          *string
	FilterDirs          *string
	ModelProviderID     *uint
	ExtractMetadata     *bool
	DetectDuplicates    *bool
	BulkModelProviderID *uint
	Goal                *string
	ScopeExts           *string
}

// Update changes the given settings only. Each is a column write, so a scan or
// run updating the same row concurrently loses nothing.
func (s *Service) Update(ctx context.Context, id uint, in UpdateInput) (*View, error) {
	if _, err := s.load(ctx, id); err != nil {
		return nil, err
	}
	ss := s.q.ScanSession
	var set []field.AssignExpr
	if in.AllowVision != nil {
		set = append(set, ss.AllowVision.Value(*in.AllowVision))
	}
	if in.AllowReadFile != nil {
		set = append(set, ss.AllowReadFile.Value(*in.AllowReadFile))
	}
	if in.AllowAutoCategory != nil {
		set = append(set, ss.AllowAutoCategory.Value(*in.AllowAutoCategory))
	}
	if in.AllowScript != nil {
		set = append(set, ss.AllowScript.Value(*in.AllowScript))
	}
	if in.ExcludeCategoryDirs != nil {
		set = append(set, ss.ExcludeCategoryDirs.Value(*in.ExcludeCategoryDirs))
	}
	if in.FilterMode != nil {
		mode, err := normalizeFilterMode(*in.FilterMode)
		if err != nil {
			return nil, err
		}
		set = append(set, ss.FilterMode.Value(mode))
	}
	if in.FilterDirs != nil {
		set = append(set, ss.FilterDirs.Value(*in.FilterDirs))
	}
	if in.ModelProviderID != nil {
		if err := s.modelExists(ctx, *in.ModelProviderID, false); err != nil {
			return nil, err
		}
		set = append(set, ss.ModelProviderID.Value(*in.ModelProviderID))
	}
	if in.ExtractMetadata != nil {
		set = append(set, ss.ExtractMetadata.Value(*in.ExtractMetadata))
	}
	if in.DetectDuplicates != nil {
		set = append(set, ss.DetectDuplicates.Value(*in.DetectDuplicates))
	}
	if in.BulkModelProviderID != nil {
		if err := s.modelExists(ctx, *in.BulkModelProviderID, true); err != nil {
			return nil, err
		}
		set = append(set, ss.BulkModelProviderID.Value(*in.BulkModelProviderID))
	}
	if in.Goal != nil {
		set = append(set, ss.Goal.Value(strings.TrimSpace(*in.Goal)))
	}
	if in.ScopeExts != nil {
		set = append(set, ss.ScopeExts.Value(normalizeExts(*in.ScopeExts)))
	}
	if len(set) > 0 {
		if _, err := ss.WithContext(ctx).Where(ss.ID.Eq(id)).UpdateSimple(set...); err != nil {
			return nil, err
		}
		db.Emit(id, 0, db.EvSessionUpdated, struct{}{})
	}
	return s.Get(ctx, id)
}

// Delete stops the session's scan and runs, then removes the session and
// everything that belongs to it in one transaction. Deleting a session that is
// already gone succeeds.
func (s *Service) Delete(ctx context.Context, id uint) error {
	s.run.StopSession(id)
	return s.q.Transaction(func(tx *query.Query) error {
		if _, err := tx.FileEntry.WithContext(ctx).Where(tx.FileEntry.ScanSessionID.Eq(id)).Delete(); err != nil {
			return err
		}
		if _, err := tx.SessionEvent.WithContext(ctx).Where(tx.SessionEvent.SessionID.Eq(id)).Delete(); err != nil {
			return err
		}
		if _, err := tx.ExecOp.WithContext(ctx).Where(tx.ExecOp.SessionID.Eq(id)).Delete(); err != nil {
			return err
		}
		if _, err := tx.AgentRun.WithContext(ctx).Where(tx.AgentRun.SessionID.Eq(id)).Delete(); err != nil {
			return err
		}
		if _, err := tx.PlanRule.WithContext(ctx).Where(tx.PlanRule.SessionID.Eq(id)).Delete(); err != nil {
			return err
		}
		if _, err := tx.SessionPlan.WithContext(ctx).Where(tx.SessionPlan.SessionID.Eq(id)).Delete(); err != nil {
			return err
		}
		if _, err := tx.ChatTurn.WithContext(ctx).Where(tx.ChatTurn.SessionID.Eq(id)).Delete(); err != nil {
			return err
		}
		m := tx.MemoryItem
		if _, err := m.WithContext(ctx).Where(m.Scope.Eq(db.MemoryScopeSession), m.ScopeID.Eq(id)).Delete(); err != nil {
			return err
		}
		_, err := tx.ScanSession.WithContext(ctx).Where(tx.ScanSession.ID.Eq(id)).Delete()
		return err
	})
}

// Rescan walks the session's scan path again; decisions of unchanged entries
// are kept by the scanner.
func (s *Service) Rescan(ctx context.Context, id uint) (*View, error) {
	sess, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.run.ResolveFSConfig(ctx, sess); err != nil {
		return nil, err
	}
	ar := s.q.AgentRun
	n, err := ar.WithContext(ctx).Where(ar.SessionID.Eq(id), ar.Status.Eq(db.RunRunning)).Count()
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, errBusy
	}
	// Waits so the previous scan's last writes cannot land after the reset.
	s.run.StopScan(id)
	ss := s.q.ScanSession
	if _, err := ss.WithContext(ctx).Where(ss.ID.Eq(id)).UpdateSimple(ss.RootPath.Value(run.ScanRoot(sess.ScanPath))); err != nil {
		return nil, err
	}
	if err := s.repo.BeginScan(id); err != nil {
		return nil, err
	}
	if sess, err = s.load(ctx, id); err != nil {
		return nil, err
	}
	if err := s.run.StartScan(ctx, sess, false); err != nil {
		_ = s.repo.FailScan(id, err.Error())
		return nil, err
	}
	db.Emit(id, 0, db.EvSessionUpdated, struct{}{})
	return s.view(ctx, sess)
}

// Stats is the category distribution and the totals shown on a session's
// overview.
type Stats struct {
	Distribution []db.CategoryDistribution
	TotalFiles   int
	TaggedFiles  int
	PlannedOps   int
	TotalTokens  int
}

func (s *Service) Stats(ctx context.Context, id uint) (*Stats, error) {
	sess, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	dist, err := s.repo.GetSessionStats(id, sess.FilesystemID)
	if err != nil {
		return nil, err
	}
	t, err := s.tokens(ctx, id)
	if err != nil {
		return nil, err
	}
	return &Stats{Distribution: dist, TotalFiles: sess.TotalFiles, TaggedFiles: sess.TaggedFiles, PlannedOps: sess.PlannedOps, TotalTokens: t.Total}, nil
}

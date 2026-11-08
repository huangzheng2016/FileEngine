package agent

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"FileEngine/internal/db"
)

// memoryBlockTokenBudget bounds the always-on "## 记忆" prompt block.
const memoryBlockTokenBudget = 1500

// memoryPathRelatedMax caps the path-specific items injected beyond the budget.
const memoryPathRelatedMax = 8

var memoryKindLabel = map[string]string{
	db.MemoryPreference: "偏好",
	db.MemoryConvention: "约定",
	db.MemoryCorrection: "纠正",
	db.MemoryFact:       "事实",
	db.MemoryNote:       "笔记",
}

// memoryItemPath returns the source path an item is about: its Path, or the
// subject of a "path:" key. Lower-cased (keys are), no leading slash.
func memoryItemPath(m db.MemoryItem) string {
	p := m.Path
	if p == "" {
		if rest, ok := strings.CutPrefix(m.Key, "path:"); ok {
			p = rest
		}
	}
	return strings.ToLower(strings.Trim(p, "/"))
}

// memoryPathRelated reports whether an item's path is one of the unit's
// directory prefixes (the unit lies at or below the item's path).
func memoryPathRelated(m db.MemoryItem, unitPaths []string) bool {
	ip := memoryItemPath(m)
	if ip == "" || ip == "." {
		return false
	}
	for _, u := range unitPaths {
		u = strings.ToLower(strings.Trim(u, "/"))
		if u == ip || strings.HasPrefix(u, ip+"/") {
			return true
		}
	}
	return false
}

// memoryRank orders the general section: pinned, then global preferences,
// then filesystem conventions and corrections, then everything else.
func memoryRank(m db.MemoryItem) int {
	switch {
	case m.Pinned:
		return 0
	case m.Scope == db.MemoryScopeGlobal:
		return 1
	case m.Kind == db.MemoryConvention || m.Kind == db.MemoryCorrection:
		return 2
	default:
		return 3
	}
}

func memoryRecency(m db.MemoryItem) time.Time {
	if m.LastUsedAt != nil && m.LastUsedAt.After(m.UpdatedAt) {
		return *m.LastUsedAt
	}
	return m.UpdatedAt
}

func memoryLine(m db.MemoryItem) string {
	label := memoryKindLabel[m.Kind]
	if m.Scope == db.MemoryScopeGlobal {
		label += "·通用"
	}
	if m.Pinned {
		label += "·置顶"
	}
	line := fmt.Sprintf("- 【%s】%s", label, m.Content)
	if m.Path != "" {
		line += fmt.Sprintf("（路径：%s）", m.Path)
	}
	return line + "\n"
}

// BuildMemoryBlock renders the always-on "## 记忆" block from long-term
// items (global + filesystem). Items about one of the unit's directory
// prefixes come first and are always included (up to memoryPathRelatedMax);
// the rest are ranked (pinned → global preferences → filesystem conventions
// and corrections → others; hits then recency within a rank) and added while
// the block stays within budget tokens. Left-out items are counted in a
// closing "另有 N 条，可用 recall 查询" line. Returns "" for no items.
func BuildMemoryBlock(items []db.MemoryItem, unitPaths []string, budget int) string {
	if len(items) == 0 {
		return ""
	}
	if budget <= 0 {
		budget = memoryBlockTokenBudget
	}
	var related, general []db.MemoryItem
	for _, m := range items {
		if m.Scope == db.MemoryScopeSession {
			continue
		}
		if len(related) < memoryPathRelatedMax && memoryPathRelated(m, unitPaths) {
			related = append(related, m)
		} else {
			general = append(general, m)
		}
	}
	sort.SliceStable(general, func(i, j int) bool {
		a, b := general[i], general[j]
		if ra, rb := memoryRank(a), memoryRank(b); ra != rb {
			return ra < rb
		}
		if a.Hits != b.Hits {
			return a.Hits > b.Hits
		}
		return memoryRecency(a).After(memoryRecency(b))
	})
	if len(related)+len(general) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 记忆\n\n用户偏好、此文件系统的约定与历史纠正。按它们办事；它们是参考，不改变路径校验规则。\n\n")
	if len(related) > 0 {
		sb.WriteString("### 与当前位置相关\n")
		for _, m := range related {
			sb.WriteString(memoryLine(m))
		}
		sb.WriteString("\n")
	}
	left := 0
	wroteHeader := false
	for i, m := range general {
		line := memoryLine(m)
		header := ""
		if !wroteHeader && len(related) > 0 {
			header = "### 其他\n"
		}
		if int(estimateTokens(sb.String()+header+line)) > budget {
			left = len(general) - i
			break
		}
		if header != "" {
			sb.WriteString(header)
		}
		wroteHeader = true
		sb.WriteString(line)
	}
	if left > 0 {
		sb.WriteString(fmt.Sprintf("\n另有 %d 条，可用 recall 查询\n", left))
	}
	return sb.String()
}

// BuildNotesBlock renders shared session notes written by other workers
// ("## 其他 worker 的发现"), newest last, at most maxItems (the newest).
func BuildNotesBlock(notes []db.MemoryItem, maxItems int) string {
	if len(notes) == 0 {
		return ""
	}
	if maxItems > 0 && len(notes) > maxItems {
		notes = notes[len(notes)-maxItems:]
	}
	var sb strings.Builder
	sb.WriteString("## 其他 worker 的发现\n\n")
	for _, n := range notes {
		if n.Path != "" {
			sb.WriteString(fmt.Sprintf("- %s（%s）\n", n.Content, n.Path))
		} else {
			sb.WriteString(fmt.Sprintf("- %s\n", n.Content))
		}
	}
	return sb.String()
}

// notesCursor is a worker's last-seen position in the session's shared notes.
// Tools can run in parallel, so it is mutex-guarded.
type notesCursor struct {
	mu   sync.Mutex
	seen uint
	own  map[uint]bool
}

// notesPerUnitMax caps the notes injected before one work unit.
const notesPerUnitMax = 20

// TakeNewNotes returns the "## 其他 worker 的发现" block of notes written by
// other workers since this worker's last call, and advances the cursor. The
// tagging loop calls it before each new work unit.
func (tb *ToolBuilder) TakeNewNotes() (string, error) {
	tb.notes.mu.Lock()
	defer tb.notes.mu.Unlock()
	notes, err := tb.repo.NotesSince(tb.sessionID, tb.notes.seen, 200)
	if err != nil {
		return "", err
	}
	var others []db.MemoryItem
	for _, n := range notes {
		if n.ID > tb.notes.seen {
			tb.notes.seen = n.ID
		}
		if !tb.notes.own[n.ID] {
			others = append(others, n)
		}
	}
	return BuildNotesBlock(others, notesPerUnitMax), nil
}

// SetUnitRoot sets the directory of the work unit being processed; script
// set_category keeps file paths relative to it.
func (tb *ToolBuilder) SetUnitRoot(dir string) {
	tb.unitRoot = strings.Trim(normalizePath(dir), "/")
}

func (tb *ToolBuilder) emitMemoryUpdated(m *db.MemoryItem) {
	db.Emit(tb.sessionID, tb.RunID, db.EvMemoryUpdated, db.MemoryUpdatedData{RunID: tb.RunID, Scope: m.Scope, ID: m.ID})
}

// ============ Memory tools ============

type RememberInput struct {
	Scope   string `json:"scope" jsonschema_description:"filesystem = 此文件系统的长期约定/纠正；global = 用户的通用偏好（对所有文件系统生效）"`
	Kind    string `json:"kind" jsonschema_description:"preference（偏好）/ convention（约定）/ correction（纠正）/ fact（事实）"`
	Key     string `json:"key" jsonschema_description:"主题键，短而规范，如 ext:.psd、path:照片/2023、pref:folder-language；同一范围内相同 key 会覆盖旧内容"`
	Content string `json:"content" jsonschema_description:"记忆内容，中文一句话，300 字以内"`
	Path    string `json:"path,omitempty" jsonschema_description:"相关的源路径（可选），如 Design/2023"`
}

type MemoryWriteOutput struct {
	Success bool   `json:"success"`
	ID      uint   `json:"id,omitempty"`
	Created bool   `json:"created,omitempty"`
	Error   string `json:"error,omitempty"`
}

type RecallInput struct {
	Query      string `json:"query" jsonschema_description:"关键词（空格分隔，全部命中），匹配记忆的 key 和内容；可为空"`
	PathPrefix string `json:"path_prefix,omitempty" jsonschema_description:"只看与该源路径相关的记忆（路径的上级或下级），可选"`
}

type MemoryView struct {
	ID      uint   `json:"id"`
	Scope   string `json:"scope"`
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Content string `json:"content"`
	Path    string `json:"path,omitempty"`
	Pinned  bool   `json:"pinned,omitempty"`
}

type RecallOutput struct {
	Items []MemoryView `json:"items"`
}

type NoteInput struct {
	Content string `json:"content" jsonschema_description:"其他 worker 也需要知道的发现，中文一句话，300 字以内（如：Projects/foo 是一个 Go 项目根目录，整体归类）"`
	Path    string `json:"path,omitempty" jsonschema_description:"相关的源路径（可选）"`
}

func (tb *ToolBuilder) remember(ctx context.Context, in *RememberInput) (*MemoryWriteOutput, error) {
	m := &db.MemoryItem{Scope: in.Scope, Kind: in.Kind, Key: in.Key, Content: in.Content, Path: db.NormalizeMemoryPath(in.Path), Source: db.MemorySourceAgent}
	switch in.Scope {
	case db.MemoryScopeFilesystem:
		m.ScopeID = tb.filesystemID
	case db.MemoryScopeGlobal:
	default:
		return &MemoryWriteOutput{Error: "scope 只能是 filesystem 或 global（会话内共享发现请用 note）"}, nil
	}
	if in.Kind == db.MemoryNote {
		return &MemoryWriteOutput{Error: "kind 不能是 note：会话内共享发现请用 note 工具"}, nil
	}
	return tb.writeMemory(m)
}

func (tb *ToolBuilder) note(ctx context.Context, in *NoteInput) (*MemoryWriteOutput, error) {
	content := strings.TrimSpace(in.Content)
	sum := sha1.Sum([]byte(content))
	m := &db.MemoryItem{
		Scope: db.MemoryScopeSession, ScopeID: tb.sessionID, Kind: db.MemoryNote,
		Key: "note:" + hex.EncodeToString(sum[:])[:12], Content: content,
		Path: db.NormalizeMemoryPath(in.Path), Source: db.MemorySourceAgent,
	}
	out, err := tb.writeMemory(m)
	if err == nil && out.Success {
		tb.notes.mu.Lock()
		if tb.notes.own == nil {
			tb.notes.own = map[uint]bool{}
		}
		tb.notes.own[m.ID] = true
		tb.notes.mu.Unlock()
	}
	return out, err
}

// writeMemory upserts one item; validation failures are agent-recoverable
// results, DB failures are fatal.
func (tb *ToolBuilder) writeMemory(m *db.MemoryItem) (*MemoryWriteOutput, error) {
	m.Key = db.NormalizeMemoryKey(m.Key)
	m.Content = strings.TrimSpace(m.Content)
	if err := db.ValidateMemoryItem(m); err != nil {
		return &MemoryWriteOutput{Error: err.Error()}, nil
	}
	created, err := tb.repo.UpsertMemory(m)
	if err != nil {
		return nil, fmt.Errorf("write memory: %w", err)
	}
	tb.emitMemoryUpdated(m)
	return &MemoryWriteOutput{Success: true, ID: m.ID, Created: created}, nil
}

// recallLimit caps one recall result.
const recallLimit = 20

func (tb *ToolBuilder) recall(ctx context.Context, in *RecallInput) (*RecallOutput, error) {
	items, err := tb.repo.SearchMemory(db.MemorySearch{
		Query: in.Query,
		Scopes: []db.MemoryScopeRef{
			{Scope: db.MemoryScopeGlobal},
			{Scope: db.MemoryScopeFilesystem, ScopeID: tb.filesystemID},
			{Scope: db.MemoryScopeSession, ScopeID: tb.sessionID},
		},
		Limit: 200,
	})
	if err != nil {
		return nil, fmt.Errorf("recall: %w", err)
	}
	pp := strings.ToLower(db.NormalizeMemoryPath(in.PathPrefix))
	out := &RecallOutput{Items: []MemoryView{}}
	var ids []uint
	for _, m := range items {
		if pp != "" {
			ip := memoryItemPath(m)
			if ip == "" || !(pp == ip || strings.HasPrefix(pp, ip+"/") || strings.HasPrefix(ip, pp+"/")) {
				continue
			}
		}
		out.Items = append(out.Items, MemoryView{ID: m.ID, Scope: m.Scope, Kind: m.Kind, Key: m.Key, Content: m.Content, Path: m.Path, Pinned: m.Pinned})
		ids = append(ids, m.ID)
		if len(out.Items) >= recallLimit {
			break
		}
	}
	if err := tb.repo.BumpMemoryHits(ids); err != nil {
		return nil, fmt.Errorf("recall: %w", err)
	}
	return out, nil
}

// ============ Reflection ops ============

// Memory consolidation operations (mem0-style) produced by the reflection
// LLM call at the end of a tagging run.
const (
	MemoryOpAdd    = "ADD"
	MemoryOpUpdate = "UPDATE"
	MemoryOpDelete = "DELETE"
	MemoryOpNoop   = "NOOP"
)

// MemoryOp is one consolidation decision. ADD upserts by (scope, key); UPDATE
// and DELETE address an existing long-term item by ID.
type MemoryOp struct {
	Op      string `json:"op"`
	ID      uint   `json:"id,omitempty"`
	Scope   string `json:"scope,omitempty"` // filesystem (default) / global
	Kind    string `json:"kind,omitempty"`
	Key     string `json:"key,omitempty"`
	Content string `json:"content,omitempty"`
	Path    string `json:"path,omitempty"`
}

// MemoryOpsResult summarizes ApplyMemoryOps. Rejected lists ops that were
// skipped as invalid or not permitted (they never abort the batch).
type MemoryOpsResult struct {
	Added, Updated, Deleted, Noop int
	Rejected                      []string
	Changed                       []db.MemoryItem
}

// ApplyMemoryOps applies reflection ops to the long-term memory of one
// filesystem (plus global). The user is the final curator: pinned items and
// items the user wrote are never updated or deleted by reflection, and
// session notes are out of reach. Invalid ops are skipped and reported; only
// DB failures return an error.
func ApplyMemoryOps(repo *db.Repository, filesystemID uint, ops []MemoryOp) (MemoryOpsResult, error) {
	var res MemoryOpsResult
	reject := func(i int, msg string) { res.Rejected = append(res.Rejected, fmt.Sprintf("#%d %s", i+1, msg)) }
	loadOwned := func(i int, id uint) (*db.MemoryItem, bool, error) {
		m, err := repo.GetMemory(id)
		if errors.Is(err, db.ErrMemoryNotFound) {
			reject(i, fmt.Sprintf("记忆 %d 不存在", id))
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		inScope := m.Scope == db.MemoryScopeGlobal || (m.Scope == db.MemoryScopeFilesystem && m.ScopeID == filesystemID)
		if !inScope {
			reject(i, fmt.Sprintf("记忆 %d 不属于本文件系统", id))
			return nil, false, nil
		}
		if m.Pinned || m.Source == db.MemorySourceUser {
			reject(i, fmt.Sprintf("记忆 %d 由用户维护，不可改动", id))
			return nil, false, nil
		}
		return m, true, nil
	}
	for i, op := range ops {
		switch strings.ToUpper(strings.TrimSpace(op.Op)) {
		case MemoryOpNoop:
			res.Noop++
		case MemoryOpAdd:
			m := &db.MemoryItem{Scope: op.Scope, Kind: op.Kind, Key: op.Key, Content: op.Content, Path: db.NormalizeMemoryPath(op.Path), Source: db.MemorySourceReflection}
			if m.Scope == "" {
				m.Scope = db.MemoryScopeFilesystem
			}
			switch m.Scope {
			case db.MemoryScopeFilesystem:
				m.ScopeID = filesystemID
			case db.MemoryScopeGlobal:
			default:
				reject(i, "ADD 只能写 filesystem 或 global")
				continue
			}
			m.Key = db.NormalizeMemoryKey(m.Key)
			if err := db.ValidateMemoryItem(m); err != nil {
				reject(i, err.Error())
				continue
			}
			if _, err := repo.UpsertMemory(m); err != nil {
				return res, err
			}
			res.Added++
			res.Changed = append(res.Changed, *m)
		case MemoryOpUpdate:
			if _, ok, err := loadOwned(i, op.ID); err != nil {
				return res, err
			} else if !ok {
				continue
			}
			updated, err := repo.UpdateMemory(op.ID, func(m *db.MemoryItem) {
				if op.Kind != "" {
					m.Kind = op.Kind
				}
				if op.Key != "" {
					m.Key = op.Key
				}
				if op.Content != "" {
					m.Content = op.Content
				}
				if op.Path != "" {
					m.Path = db.NormalizeMemoryPath(op.Path)
				}
				m.Source = db.MemorySourceReflection
			})
			if err != nil {
				// Not found / validation / key clash: the op is bad, not the DB.
				if errors.Is(err, db.ErrMemoryNotFound) || !isDBFailure(err) {
					reject(i, err.Error())
					continue
				}
				return res, err
			}
			res.Updated++
			res.Changed = append(res.Changed, *updated)
		case MemoryOpDelete:
			m, ok, err := loadOwned(i, op.ID)
			if err != nil {
				return res, err
			}
			if !ok {
				continue
			}
			if err := repo.DeleteMemory(op.ID); err != nil && !errors.Is(err, db.ErrMemoryNotFound) {
				return res, err
			}
			res.Deleted++
			res.Changed = append(res.Changed, *m)
		default:
			reject(i, fmt.Sprintf("未知操作 %q", op.Op))
		}
	}
	return res, nil
}

// isDBFailure separates storage failures from validation errors returned by
// UpdateMemory (validation errors are plain messages built in package db).
func isDBFailure(err error) bool {
	var v *db.MemoryValidationError
	return !errors.As(err, &v)
}

// ============ Correction capture ============

// correctionSourcePrefix is the source directory a correction is keyed on:
// the first (up to) two segments of the entry's parent directory, so one
// correction covers sibling folders of the same area ("Design/2023/x" →
// "Design/2023"). The scan root yields "".
func correctionSourcePrefix(parent string) string {
	parent = strings.Trim(parent, "/")
	if parent == "" || parent == "." {
		return ""
	}
	segs := strings.Split(parent, "/")
	if len(segs) > 2 {
		segs = segs[:2]
	}
	return strings.Join(segs, "/")
}

// CorrectionMemory turns a user's manual re-targeting of an entry into a
// filesystem-scope correction memory, keyed by extension (or "dir") plus the
// source directory prefix, so the same kind of file from the same area is
// classified the user's way next time. Returns nil when there is nothing to
// learn (target cleared, or only the file name changed within the same
// directory).
func CorrectionMemory(f db.FileEntry, oldTarget, newTarget string, filesystemID uint) *db.MemoryItem {
	newTarget = db.NormalizeTargetPath(newTarget)
	oldTarget = db.NormalizeTargetPath(oldTarget)
	if newTarget == "" || newTarget == oldTarget {
		return nil
	}
	toDir := path.Dir(newTarget)
	fromDir := ""
	if oldTarget != "" {
		fromDir = path.Dir(oldTarget)
		if fromDir == toDir {
			return nil
		}
	}
	src := correctionSourcePrefix(f.ParentPath)
	srcLabel := "/" + src
	var kindKey, what string
	switch ext := strings.ToLower(path.Ext(f.Name)); {
	case f.FileType == "directory":
		kindKey, what = "dir", fmt.Sprintf("目录（如 `%s`）", f.Name)
	case ext == "" || ext == strings.ToLower(f.Name):
		kindKey, what = "noext", fmt.Sprintf("无扩展名文件（如 `%s`）", f.Name)
	default:
		kindKey, what = ext, fmt.Sprintf("`*%s`", ext)
	}
	content := fmt.Sprintf("`%s` 下的 %s 应放到 `%s`", srcLabel, what, toDir)
	if fromDir != "" {
		content += fmt.Sprintf("，而不是 `%s`", fromDir)
	}
	if r := []rune(content); len(r) > db.MemoryContentMaxRunes {
		content = string(r[:db.MemoryContentMaxRunes])
	}
	return &db.MemoryItem{
		Scope:   db.MemoryScopeFilesystem,
		ScopeID: filesystemID,
		Kind:    db.MemoryCorrection,
		Key:     fmt.Sprintf("correction:%s@%s", kindKey, srcLabel),
		Content: content,
		Path:    src,
		Source:  db.MemorySourceCorrection,
	}
}

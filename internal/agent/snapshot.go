package agent

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"FileEngine/internal/db"
)

// A run renders its shared context once and every later change reaches the
// model as a "## 运行中的变化" delta in the last message, so the cached prefix
// (tools, system, snapshot, history) stays byte-identical.

// resnapshotTokens is the delta size past which a tagging run renders a new
// snapshot: one cache miss for every worker, then short deltas again.
const resnapshotTokens = 1000

const deltaHeading = "## 运行中的变化"

type sharedSnapshot struct {
	mu   sync.Mutex
	gen  int
	ctx  SharedContext
	text string
}

func newSharedSnapshot(c SharedContext) *sharedSnapshot {
	return &sharedSnapshot{gen: 1, ctx: c, text: RenderSharedContext(c)}
}

func categoryNames(cats []db.Category) []string {
	names := make([]string, len(cats))
	for i, c := range cats {
		names[i] = c.Name
	}
	return names
}

func categoryLine(c db.Category) string {
	line := fmt.Sprintf("- %s：%s", SanitizeUntrustedName(c.Name), SanitizeUntrustedName(db.NormalizeTargetPath(c.Path)))
	if d := strings.TrimSpace(c.Description); d != "" {
		line += "；放什么：" + SanitizeUntrustedName(oneLine(d))
	}
	if st := strings.TrimSpace(c.Structure); st != "" {
		line += "；结构：" + SanitizeUntrustedName(oneLine(st))
	}
	return line
}

func ruleLine(r db.PlanRule) string {
	var conds []string
	if r.Exts != "" {
		conds = append(conds, "扩展名 "+SanitizeUntrustedName(r.Exts))
	}
	if r.NameRegex != "" {
		conds = append(conds, "文件名 ~ "+SanitizeUntrustedName(r.NameRegex))
	}
	if r.PathPrefix != "" {
		conds = append(conds, "位于 "+SanitizeUntrustedName(r.PathPrefix))
	}
	if r.FileType == "directory" {
		conds = append(conds, "目录")
	}
	if len(conds) == 0 {
		conds = append(conds, "全部")
	}
	return fmt.Sprintf("- %s：%s → %s", SanitizeUntrustedName(r.Name), strings.Join(conds, "，"), SanitizeUntrustedName(r.Target))
}

func memoryKey(m db.MemoryItem) string { return m.Scope + "\x00" + m.Key }

func planStatusLabel(p *db.SessionPlan) string {
	if p.Status == db.PlanApproved {
		return "用户已确认"
	}
	return "草稿，用户尚未确认"
}

func hasPlan(c SharedContext) bool {
	return c.Plan != nil && c.Plan.Version > 0 && strings.TrimSpace(c.Plan.Content) != ""
}

// RenderDelta describes what changed from base to cur, or "" when nothing
// did. inlinePlan puts a changed plan's text in the delta; otherwise the
// delta points at read_plan, so long plans are not repeated every turn.
func RenderDelta(base, cur SharedContext, inlinePlan bool) string {
	var lines []string
	add := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }

	if strings.TrimSpace(base.Goal) != strings.TrimSpace(cur.Goal) {
		add("- 用户目标已改为：%s", SanitizeUntrustedName(oneLine(cur.Goal)))
	}

	switch {
	case hasPlan(cur) && (!hasPlan(base) || base.Plan.Content != cur.Plan.Content):
		if inlinePlan {
			content := strings.TrimSpace(cur.Plan.Content)
			if r := []rune(content); len(r) > planPromptMaxRunes {
				content = string(r[:planPromptMaxRunes]) + "\n\n（方案过长已截断）"
			}
			add("- 整理方案已更新（%s），以此为准：\n\n%s\n", planStatusLabel(cur.Plan), content)
		} else {
			add("- 整理方案已更新（%s），用 read_plan 读取最新全文", planStatusLabel(cur.Plan))
		}
	case hasPlan(cur) && base.Plan.Status != cur.Plan.Status:
		add("- 整理方案状态变为：%s", planStatusLabel(cur.Plan))
	case hasPlan(base) && !hasPlan(cur):
		add("- 整理方案已清空")
	}

	baseCats := map[string]db.Category{}
	for _, c := range base.Categories {
		baseCats[c.Name] = c
	}
	curCats := sortedCategories(cur.Categories)
	curNames := map[string]bool{}
	for _, c := range curCats {
		curNames[c.Name] = true
		old, existed := baseCats[c.Name]
		switch {
		case !existed:
			add("- 新增分类 %s", strings.TrimPrefix(categoryLine(c), "- "))
		case categoryLine(old) != categoryLine(c):
			add("- 分类已改为 %s", strings.TrimPrefix(categoryLine(c), "- "))
		}
	}
	for _, c := range sortedCategories(base.Categories) {
		if !curNames[c.Name] {
			add("- 分类 %s 已删除", SanitizeUntrustedName(c.Name))
		}
	}

	baseRules := map[string]string{}
	for _, r := range base.Rules {
		baseRules[r.Name] = ruleLine(r)
	}
	curRules := map[string]bool{}
	for _, r := range cur.Rules {
		curRules[r.Name] = true
		old, existed := baseRules[r.Name]
		switch {
		case !existed:
			add("- 新增规则 %s", strings.TrimPrefix(ruleLine(r), "- "))
		case old != ruleLine(r):
			add("- 规则已改为 %s", strings.TrimPrefix(ruleLine(r), "- "))
		}
	}
	var gone []string
	for _, r := range base.Rules {
		if !curRules[r.Name] {
			gone = append(gone, r.Name)
		}
	}
	sort.Strings(gone)
	for _, n := range gone {
		add("- 规则 %s 已删除", SanitizeUntrustedName(n))
	}

	baseMem := map[string]db.MemoryItem{}
	for _, m := range base.Memory {
		baseMem[memoryKey(m)] = m
	}
	curMem := append([]db.MemoryItem(nil), cur.Memory...)
	sort.SliceStable(curMem, func(i, j int) bool { return memoryLess(curMem[i], curMem[j]) })
	seenMem := map[string]bool{}
	for _, m := range curMem {
		seenMem[memoryKey(m)] = true
		if old, ok := baseMem[memoryKey(m)]; !ok || old.Content != m.Content || old.Path != m.Path || old.Kind != m.Kind {
			add("%s", strings.TrimSuffix(memoryLine(m), "\n"))
		}
	}
	var goneMem []string
	for _, m := range base.Memory {
		if !seenMem[memoryKey(m)] {
			goneMem = append(goneMem, SanitizeUntrustedName(m.Key))
		}
	}
	sort.Strings(goneMem)
	for _, k := range goneMem {
		add("- 记忆 %s 已删除", k)
	}

	if len(lines) == 0 {
		return ""
	}
	return deltaHeading + "\n\n以下变化发生在共享上下文生成之后；与共享上下文冲突时以这里为准。\n\n" + strings.Join(lines, "\n") + "\n\n"
}

func sortedCategories(cats []db.Category) []db.Category {
	out := append([]db.Category(nil), cats...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// view loads the current shared state and returns the snapshot text with the
// delta against it. A delta past resnapshotTokens replaces the snapshot
// instead (one cache miss for every worker) and the delta is empty. The text
// and the delta always belong together.
func (s *sharedSnapshot) view(load func() (SharedContext, error), inlinePlan bool) (text, delta string, cur SharedContext, err error) {
	cur, err = load()
	if err != nil {
		return "", "", cur, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delta = RenderDelta(s.ctx, cur, inlinePlan)
	if estimateTokens(delta) > resnapshotTokens {
		s.gen++
		s.ctx, s.text = cur, RenderSharedContext(cur)
		delta = ""
	}
	return s.text, delta, cur, nil
}

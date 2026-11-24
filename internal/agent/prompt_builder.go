package agent

import (
	"fmt"
	"sort"
	"strings"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"

	"github.com/cloudwego/eino/schema"
)

func buildCategorySummary(usages []db.CategoryUsage) string {
	if len(usages) == 0 {
		return ""
	}
	cats := make([]model.Category, len(usages))
	for i, u := range usages {
		cats[i] = model.Category{Name: u.Name, Path: u.Path, Description: u.Description, Structure: u.Structure}
	}
	return renderCategoryList(cats)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

const planPromptMaxRunes = 8000

// SharedContext renders as one message that stays byte-identical across all
// workers and units of a run until its content changes: no counts,
// usage-based ordering or per-unit data.
type SharedContext struct {
	Goal       string
	Plan       *model.SessionPlan // nil or Version 0 = no plan
	Memory     []model.MemoryItem
	Categories []model.Category
	Rules      []model.PlanRule
}

func LoadSharedContext(repo *db.Repository, session *model.ScanSession) (SharedContext, error) {
	c := SharedContext{Goal: session.Goal}
	var err error
	if c.Plan, err = repo.GetPlan(session.ID); err != nil {
		return c, fmt.Errorf("shared context: plan: %w", err)
	}
	if c.Memory, err = repo.ListLongTermMemory(session.FilesystemID); err != nil {
		return c, fmt.Errorf("shared context: memory: %w", err)
	}
	if c.Categories, err = repo.ListCategories(session.FilesystemID); err != nil {
		return c, fmt.Errorf("shared context: categories: %w", err)
	}
	if c.Rules, err = repo.ListRules(session.ID); err != nil {
		return c, fmt.Errorf("shared context: rules: %w", err)
	}
	return c, nil
}

func RenderSharedContext(c SharedContext) string {
	var sb strings.Builder
	if g := strings.TrimSpace(c.Goal); g != "" {
		sb.WriteString("## 用户目标\n\n" + g + "\n\n")
	}
	if hasPlan(c) {
		sb.WriteString(fmt.Sprintf("## 整理方案（%s）\n\n", planStatusLabel(c.Plan)))
		content := strings.TrimSpace(c.Plan.Content)
		if r := []rune(content); len(r) > planPromptMaxRunes {
			content = string(r[:planPromptMaxRunes]) + "\n\n（方案过长，已截断）"
		}
		sb.WriteString(content + "\n\n")
	}
	sb.WriteString(renderCategoryList(c.Categories))
	if r := renderRules(c.Rules); r != "" {
		sb.WriteString(r)
	}
	if block := BuildMemoryBlock(c.Memory, nil, memoryBlockTokenBudget); block != "" {
		sb.WriteString(block + "\n")
	}
	return strings.TrimRight(sb.String(), "\n") + "\n"
}

// Never usage counts: they change every unit.
func renderCategoryList(cats []model.Category) string {
	if len(cats) == 0 {
		return "## 分类\n\n（还没有分类）\n\n"
	}
	var sb strings.Builder
	sb.WriteString("## 分类\n\n")
	for _, c := range sortedCategories(cats) {
		sb.WriteString(categoryLine(c) + "\n")
	}
	return sb.String() + "\n"
}

// Rules are keyed by name and carry no ids or positions: inserting a rule
// must not rewrite the lines after it.
func renderRules(rules []model.PlanRule) string {
	if len(rules) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 规则（按顺序，先匹配者生效）\n\n")
	for _, r := range rules {
		sb.WriteString(ruleLine(r) + "\n")
	}
	return sb.String() + "\n"
}

func BuildSharedContext(repo *db.Repository, session *model.ScanSession) (string, error) {
	c, err := LoadSharedContext(repo, session)
	if err != nil {
		return "", err
	}
	return RenderSharedContext(c), nil
}

// SharedContextMessage is message 0 of every request: the cache breakpoint
// that every later message extends.
func SharedContextMessage(text string) *schema.Message {
	return modelfactory.MarkCacheBreakpoint(schema.UserMessage("# 会话共享上下文\n\n" + text))
}

type UnitContext struct {
	ParentChain []model.FileEntry
	Memory      []model.MemoryItem // only items about the unit's location are rendered
	UnitPaths   []string
	Notes       string
}

func sourceAncestors(p string) []string {
	var out []string
	p = strings.Trim(p, "/")
	for {
		i := strings.LastIndex(p, "/")
		if i <= 0 {
			return out
		}
		p = p[:i]
		out = append(out, p)
	}
}

func LoadUnitContext(repo *db.Repository, session *model.ScanSession, unitPaths []string, notes string) (UnitContext, error) {
	u := UnitContext{UnitPaths: unitPaths, Notes: notes}
	seen := map[string]bool{}
	var ancestors []string
	for _, p := range unitPaths {
		for _, a := range append([]string{strings.Trim(p, "/")}, sourceAncestors(p)...) {
			if a != "" && !seen[a] {
				seen[a] = true
				ancestors = append(ancestors, a)
			}
		}
	}
	var err error
	if u.ParentChain, err = repo.GetEntriesByPaths(session.ID, ancestors); err != nil {
		return u, fmt.Errorf("unit context: parent chain: %w", err)
	}
	if u.Memory, err = repo.ListLongTermMemory(session.FilesystemID); err != nil {
		return u, fmt.Errorf("unit context: memory: %w", err)
	}
	return u, nil
}

func RenderUnitContext(u UnitContext) string {
	var sb strings.Builder
	if chain := renderParentChain(u.ParentChain); chain != "" {
		sb.WriteString(chain + "\n")
	}
	var related []model.MemoryItem
	for _, m := range u.Memory {
		if len(related) < memoryPathRelatedMax && memoryPathRelated(m, u.UnitPaths) {
			related = append(related, m)
		}
	}
	if len(related) > 0 {
		sb.WriteString("## 与此位置相关的记忆\n\n")
		for _, m := range related {
			sb.WriteString(memoryLine(m))
		}
		sb.WriteString("\n")
	}
	if n := strings.TrimSpace(u.Notes); n != "" {
		sb.WriteString(n + "\n\n")
	}
	return sb.String()
}

func renderParentChain(entries []model.FileEntry) string {
	var described []model.FileEntry
	for _, e := range entries {
		if strings.TrimSpace(e.Description) != "" {
			described = append(described, e)
		}
	}
	if len(described) == 0 {
		return ""
	}
	sort.SliceStable(described, func(i, j int) bool { return len(described[i].OriginalPath) < len(described[j].OriginalPath) })
	var sb strings.Builder
	sb.WriteString("## 上级目录\n\n")
	for _, e := range described {
		sb.WriteString(fmt.Sprintf("- %s：%s\n", SanitizeUntrustedName(e.OriginalPath), SanitizeUntrustedName(oneLine(e.Description))))
	}
	return sb.String()
}

const unitListingMax = 100

func listingBlock(title string, rows []model.FileEntry, total int) string {
	shown := rows
	if len(shown) > unitListingMax {
		shown = shown[:unitListingMax]
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %s（%s）\n\n", title, listColumns))
	sb.WriteString(WrapUntrusted(title, entryRows(shown, false)))
	sb.WriteString("\n")
	if total > len(shown) {
		sb.WriteString(fmt.Sprintf("（只列出前 %d 项，共 %d 项；其余用 list_entries 翻页查看，或用规则批量处理）\n", len(shown), total))
	}
	return sb.String()
}

func displayPath(p string) string {
	if p == "" {
		return "（扫描根目录）"
	}
	return SanitizeUntrustedName(p)
}

func buildDirUnitPrompt(dir *model.FileEntry, children []model.FileEntry, sig DirSignals, unitCtx string) string {
	var sb strings.Builder
	sb.WriteString("# 当前任务：决定一个目录\n\n")
	sb.WriteString("路径：" + displayPath(dir.OriginalPath) + "\n")
	if d := strings.TrimSpace(dir.Description); d != "" {
		sb.WriteString("描述：" + SanitizeUntrustedName(oneLine(d)) + "\n")
	}
	sb.WriteString("\n## 信号\n\n" + WrapUntrusted("目录信号", strings.TrimRight(sig.Render(), "\n")) + "\n")
	sb.WriteString(listingBlock("内容", children, len(children)))
	sb.WriteString("\n" + unitCtx)
	sb.WriteString(`## 要做的事

用 decide 决定这个目录（只这一条决定，写好就停）：
- 一个完整的整体（项目、软件、相册或活动、数据集、备份集）→ action=unit，new_path 写它移动后的完整路径
- 只是分组或杂物目录 → action=container；子目录和散文件之后作为单独任务处理，现在不要处理它们
- 不该整理 → action=skip
信号里有项目标记时几乎一定是 unit。看懂了目录用途时用 describe 给它写一句描述。
`)
	return sb.String()
}

func buildGroupUnitPrompt(parent string, files []model.FileEntry, unitCtx string, hints map[uint]string) string {
	var sb strings.Builder
	sb.WriteString("# 当前任务：归类一个目录里的散文件\n\n")
	sb.WriteString(fmt.Sprintf("目录：%s；待决定的文件：%d 个\n\n", displayPath(parent), len(files)))
	sb.WriteString(listingBlock("文件", files, len(files)))
	var hintLines []string
	for i, f := range files {
		if i >= unitListingMax {
			break
		}
		if h := hints[f.ID]; h != "" {
			hintLines = append(hintLines, fmt.Sprintf("- %s：%s", SanitizeUntrustedName(f.Name), SanitizeUntrustedName(h)))
		}
	}
	if len(hintLines) > 0 {
		sb.WriteString("\n## 压缩包内容摘要\n\n" + WrapUntrusted("压缩包摘要", strings.Join(hintLines, "\n")) + "\n")
	}
	sb.WriteString("\n" + unitCtx)
	sb.WriteString(`## 要做的事

为每个文件做决定：用 decide 一批写入（action=move，new_path 含文件名，每条带 reason，拿不准时 confidence 低于 0.6）；不该整理的 action=skip。同类文件约 20 个以上时先用规则批量处理（add_rule 带 path_prefix 限定到本目录，再 apply_rules），剩下的再逐个决定。
`)
	return sb.String()
}

func buildVersionUnitPrompt(parent string, g VersionGroup, suggested string, unitCtx string) string {
	var sb strings.Builder
	sb.WriteString("# 当前任务：归类一组版本\n\n")
	sb.WriteString(fmt.Sprintf("目录：%s；%d 个版本：\n\n", displayPath(parent), len(g.Members)))
	var rows []string
	rows = append(rows, "软件\t"+SanitizeUntrustedName(g.Name))
	for _, m := range g.Members {
		rows = append(rows, fmt.Sprintf("%s\t版本 %s\t%s", SanitizeUntrustedName(m.Name), SanitizeUntrustedName(m.Version), SanitizeUntrustedName(m.Path)))
	}
	sb.WriteString(WrapUntrusted("版本组", strings.Join(rows, "\n")) + "\n\n")
	sb.WriteString("建议目标：" + SanitizeUntrustedName(suggested) + "/<版本>/<原名>（用 decide：目录 action=unit，文件 action=move）\n\n")
	sb.WriteString(unitCtx)
	sb.WriteString("## 要做的事\n\n一次决定完整组版本，各版本用同一种路径格式；明显不是同一软件的成员按普通文件单独决定。\n")
	return sb.String()
}

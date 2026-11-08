package agent

import (
	"fmt"
	"sort"
	"strings"

	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"

	"github.com/cloudwego/eino/schema"
)

// buildCategorySummary renders the complete category list (name order, no
// usage counts) for guard rejections.
func buildCategorySummary(usages []db.CategoryUsage) string {
	if len(usages) == 0 {
		return ""
	}
	cats := make([]db.Category, len(usages))
	for i, u := range usages {
		cats[i] = db.Category{Name: u.Name, Path: u.Path, Description: u.Description, Structure: u.Structure}
	}
	return renderCategoryList(cats)
}

// oneLine collapses whitespace (newlines included) to single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// planPromptMaxRunes caps the plan injected into a prompt; read_plan returns
// the full text.
const planPromptMaxRunes = 8000

// SharedContext is the run-stable context every agent request of a session
// carries: goal, plan, categories, the memory core block and the rules. It is
// rendered as ONE message that stays byte-identical across all workers and
// units of a run until its content changes (cache affinity): no counts, no
// usage-based ordering, no per-unit data.
type SharedContext struct {
	Goal       string
	Plan       *db.SessionPlan // nil or Version 0 = no plan
	Memory     []db.MemoryItem // long-term (global + filesystem)
	Categories []db.Category
	Rules      []db.PlanRule
}

// LoadSharedContext gathers the shared context of a session.
func LoadSharedContext(repo *db.Repository, session *db.ScanSession) (SharedContext, error) {
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

// RenderSharedContext renders the shared context block; empty sections are
// omitted and the goal leads.
func RenderSharedContext(c SharedContext) string {
	var sb strings.Builder
	if g := strings.TrimSpace(c.Goal); g != "" {
		sb.WriteString("## 用户目标\n\n" + g + "\n\n")
	}
	if c.Plan != nil && c.Plan.Version > 0 && strings.TrimSpace(c.Plan.Content) != "" {
		status := "草稿，用户尚未确认"
		if c.Plan.Status == db.PlanApproved {
			status = "用户已确认"
		}
		sb.WriteString(fmt.Sprintf("## 整理方案（%s）\n\n", status))
		content := strings.TrimSpace(c.Plan.Content)
		if r := []rune(content); len(r) > planPromptMaxRunes {
			content = string(r[:planPromptMaxRunes]) + "\n\n（方案过长已截断，用 read_plan 读取全文）"
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

// renderCategoryList lists every category sorted by name, with what belongs
// there and its structure — never usage counts (they change every unit).
func renderCategoryList(cats []db.Category) string {
	if len(cats) == 0 {
		return "## 分类\n\n（还没有分类）\n\n"
	}
	sorted := append([]db.Category(nil), cats...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var sb strings.Builder
	sb.WriteString("## 分类\n\n")
	for _, c := range sorted {
		sb.WriteString(fmt.Sprintf("- %s：%s", c.Name, db.NormalizeTargetPath(c.Path)))
		if d := strings.TrimSpace(c.Description); d != "" {
			sb.WriteString("；放什么：" + oneLine(d))
		}
		if st := strings.TrimSpace(c.Structure); st != "" {
			sb.WriteString("；结构：" + oneLine(st))
		}
		sb.WriteString("\n")
	}
	return sb.String() + "\n"
}

// renderRules lists the rules in application order (without match counts).
func renderRules(rules []db.PlanRule) string {
	if len(rules) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 规则（按顺序，先匹配者生效；已自动应用到未决定的条目）\n\n")
	for _, r := range rules {
		var conds []string
		if r.Exts != "" {
			conds = append(conds, "扩展名 "+r.Exts)
		}
		if r.NameRegex != "" {
			conds = append(conds, "文件名 ~ "+r.NameRegex)
		}
		if r.PathPrefix != "" {
			conds = append(conds, "位于 "+r.PathPrefix)
		}
		if r.FileType == "directory" {
			conds = append(conds, "目录")
		}
		if len(conds) == 0 {
			conds = append(conds, "全部")
		}
		sb.WriteString(fmt.Sprintf("%d. [#%d] %s：%s → %s\n", r.Position, r.ID, r.Name, strings.Join(conds, "，"), r.Target))
	}
	return sb.String() + "\n"
}

// BuildSharedContext loads and renders the shared context block.
func BuildSharedContext(repo *db.Repository, session *db.ScanSession) (string, error) {
	c, err := LoadSharedContext(repo, session)
	if err != nil {
		return "", err
	}
	return RenderSharedContext(c), nil
}

// SharedContextMessage wraps the shared context block as the first user
// message of a request, marked as a cache breakpoint.
func SharedContextMessage(text string) *schema.Message {
	return modelfactory.MarkCacheBreakpoint(schema.UserMessage("# 会话共享上下文\n\n" + text))
}

// UnitContext is the per-unit part of a request: described ancestors, memory
// about this location and new notes from other workers.
type UnitContext struct {
	ParentChain []db.FileEntry
	Memory      []db.MemoryItem // only items about the unit's location are rendered
	UnitPaths   []string
	Notes       string
}

// sourceAncestors returns the ancestor directories of a source path
// ("a/b/c.txt" → [a/b, a]); the root is excluded.
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

// LoadUnitContext gathers the per-unit context for unitPaths (a directory
// unit: its path; a folder group: its parent directory).
func LoadUnitContext(repo *db.Repository, session *db.ScanSession, unitPaths []string, notes string) (UnitContext, error) {
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

// RenderUnitContext renders the per-unit context sections ("" when empty).
func RenderUnitContext(u UnitContext) string {
	var sb strings.Builder
	if chain := renderParentChain(u.ParentChain); chain != "" {
		sb.WriteString(chain + "\n")
	}
	var related []db.MemoryItem
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

// renderParentChain lists the described ancestors top-down under "## 上级目录".
func renderParentChain(entries []db.FileEntry) string {
	var described []db.FileEntry
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

// unitListingMax is how many children a unit prompt lists inline.
const unitListingMax = 100

func listingBlock(title string, rows []db.FileEntry, total int) string {
	shown := rows
	if len(shown) > unitListingMax {
		shown = shown[:unitListingMax]
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %s（%s）\n\n", title, listColumns))
	sb.WriteString(WrapUntrusted(title, entryRows(shown)))
	sb.WriteString("\n")
	if total > len(shown) {
		sb.WriteString(fmt.Sprintf("（只列出前 %d 项，共 %d 项；其余用 list_files 翻页，或用规则/脚本批量处理）\n", len(shown), total))
	}
	return sb.String()
}

func displayPath(p string) string {
	if p == "" {
		return "（扫描根目录）"
	}
	return SanitizeUntrustedName(p)
}

// buildDirUnitPrompt asks for one directory decision.
func buildDirUnitPrompt(dir *db.FileEntry, children []db.FileEntry, sig DirSignals, unitCtx string) string {
	var sb strings.Builder
	sb.WriteString("# 当前任务：决定一个目录\n\n")
	sb.WriteString("路径：" + displayPath(dir.OriginalPath) + "\n")
	if d := strings.TrimSpace(dir.Description); d != "" {
		sb.WriteString("描述：" + SanitizeUntrustedName(oneLine(d)) + "\n")
	}
	sb.WriteString("\n## 信号\n\n" + sig.Render() + "\n")
	sb.WriteString(listingBlock("内容", children, len(children)))
	sb.WriteString("\n" + unitCtx)
	sb.WriteString(`## 要做的事

用 decide_dirs 决定这个目录：
- 是完整单元（项目、软件、相册/活动、数据集、备份集…）→ role=unit，给出目标路径，整体移动
- 是分组/杂物目录 → role=container（不移动；子目录和散文件之后会作为单独任务交给 worker）
- 不该动 → role=skip
信号里有项目标记时几乎一定是 unit。决定 container 时不要处理子项。
`)
	return sb.String()
}

// buildGroupUnitPrompt asks for decisions on the loose files of one folder.
func buildGroupUnitPrompt(parent string, files []db.FileEntry, unitCtx string, hints map[uint]string) string {
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

把这些文件逐个决定：set_targets 批量设目标（每条带 reason；拿不准时 confidence 低于 0.6），不该动的用 decide_dirs 设 skip。
超过约 20 个同类文件时先 add_rule + apply_rules（或 run_script），剩下的再逐个处理。独立的工具调用请在同一轮并行发出。
`)
	return sb.String()
}

// buildVersionUnitPrompt asks for one version group.
func buildVersionUnitPrompt(parent string, g VersionGroup, suggested string, unitCtx string) string {
	var sb strings.Builder
	sb.WriteString("# 当前任务：归类一组版本\n\n")
	sb.WriteString(fmt.Sprintf("目录：%s；「%s」的 %d 个版本：\n\n", displayPath(parent), SanitizeUntrustedName(g.Name), len(g.Members)))
	var rows []string
	for _, m := range g.Members {
		rows = append(rows, fmt.Sprintf("%s\t版本 %s", m.Name, m.Version))
	}
	sb.WriteString(WrapUntrusted("版本组", SanitizeUntrustedName(strings.Join(rows, " | "))) + "\n\n")
	sb.WriteString("建议目标：" + suggested + "/<版本>/<原名>（目录版本用 decide_dirs 设 unit，文件用 set_targets）\n\n")
	sb.WriteString(unitCtx)
	sb.WriteString("## 要做的事\n\n一次把整组版本决定完，保持同一命名方式；明显不是同一软件的成员单独处理。\n")
	return sb.String()
}

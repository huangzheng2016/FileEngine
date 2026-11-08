package agent

import (
	"fmt"
	"sort"
	"strings"

	"FileEngine/internal/db"
)

// buildCategorySummary renders the complete category system for the agent
// prompt: every category is listed (used and unused alike) with its
// description and structure rule, so the agent never plans against a partial
// view and knows what each category is for. Entries are sorted by usage
// descending then name ascending. usages is sorted in place.
func buildCategorySummary(usages []db.CategoryUsage) string {
	if len(usages) == 0 {
		return ""
	}
	sort.SliceStable(usages, func(i, j int) bool {
		if usages[i].FileCount != usages[j].FileCount {
			return usages[i].FileCount > usages[j].FileCount
		}
		return usages[i].Name < usages[j].Name
	})
	var sb strings.Builder
	sb.WriteString("\n## 分类体系（完整列表，含已归入计数）\n")
	for _, u := range usages {
		if u.FileCount > 0 {
			sb.WriteString(fmt.Sprintf("- %s (%s): 已归入 %d 项\n", u.Name, u.Path, u.FileCount))
		} else {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", u.Name, u.Path))
		}
		if d := strings.TrimSpace(u.Description); d != "" {
			sb.WriteString(fmt.Sprintf("  放什么：%s\n", oneLine(d)))
		}
		if st := strings.TrimSpace(u.Structure); st != "" {
			sb.WriteString(fmt.Sprintf("  目录结构：%s\n", oneLine(st)))
		}
	}
	return sb.String()
}

// oneLine collapses whitespace (newlines included) to single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// planPromptMaxRunes caps the plan injected into a prompt; read_plan returns
// the full text.
const planPromptMaxRunes = 8000

// SharedContext is everything every agent prompt (survey, tagging, review,
// chat) shares: the user's goal, the plan, long-term memory, live notes from
// other workers, the category system and the descriptions of the unit's
// parent directories.
type SharedContext struct {
	Goal       string
	Plan       *db.SessionPlan // nil or Version 0 = no plan
	Memory     []db.MemoryItem // long-term (global + filesystem) candidates
	Notes      string          // pre-rendered notes block (ToolBuilder.TakeNewNotes)
	Categories []db.CategoryUsage
	// ParentChain is the unit's ancestor directories (any order); only those
	// with a description are rendered, top-down.
	ParentChain []db.FileEntry
	// UnitPaths are the source paths of the work unit (directory path, or
	// the loose files' parent directory) — they select path-specific memory.
	UnitPaths []string
}

// RenderSharedContext renders the shared context block. Sections without
// content are omitted; the goal leads because it outranks everything else.
func RenderSharedContext(c SharedContext) string {
	var sb strings.Builder
	if g := strings.TrimSpace(c.Goal); g != "" {
		sb.WriteString("## 用户目标\n\n")
		sb.WriteString(g)
		sb.WriteString("\n\n")
	}
	if c.Plan != nil && c.Plan.Version > 0 && strings.TrimSpace(c.Plan.Content) != "" {
		status := "草稿，用户尚未确认"
		if c.Plan.Status == db.PlanApproved {
			status = "用户已确认"
		}
		sb.WriteString(fmt.Sprintf("## 整理方案（第 %d 版，%s）\n\n", c.Plan.Version, status))
		content := strings.TrimSpace(c.Plan.Content)
		if r := []rune(content); len(r) > planPromptMaxRunes {
			content = string(r[:planPromptMaxRunes]) + "\n\n（方案过长已截断，用 read_plan 读取全文）"
		}
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}
	if block := BuildMemoryBlock(c.Memory, c.UnitPaths, memoryBlockTokenBudget); block != "" {
		sb.WriteString(block)
		sb.WriteString("\n")
	}
	if n := strings.TrimSpace(c.Notes); n != "" {
		sb.WriteString(n)
		sb.WriteString("\n\n")
	}
	if cats := strings.TrimSpace(buildCategorySummary(c.Categories)); cats != "" {
		sb.WriteString(cats)
		sb.WriteString("\n\n")
	}
	if chain := renderParentChain(c.ParentChain); chain != "" {
		sb.WriteString(chain)
		sb.WriteString("\n")
	}
	return sb.String()
}

// renderParentChain lists the described ancestors top-down (shallowest
// first) under "## 上级目录".
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
		sb.WriteString(fmt.Sprintf("- %s：%s\n", e.OriginalPath, oneLine(e.Description)))
	}
	return sb.String()
}

// sourceAncestors returns the ancestor directories of a source path
// ("a/b/c.txt" → [a/b, a]); the scan root "." is excluded.
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

// LoadSharedContext gathers the shared context for one work unit of a
// session. unitPaths are the unit's source paths (a directory unit: its own
// path; a loose-file group: the files' parent directory). notes is the
// caller's pre-rendered notes block (ToolBuilder.TakeNewNotes; "" for none).
func LoadSharedContext(repo *db.Repository, session *db.ScanSession, unitPaths []string, notes string) (SharedContext, error) {
	c := SharedContext{Goal: session.Goal, Notes: notes, UnitPaths: unitPaths}
	plan, err := repo.GetPlan(session.ID)
	if err != nil {
		return c, fmt.Errorf("shared context: plan: %w", err)
	}
	c.Plan = plan
	if c.Memory, err = repo.ListLongTermMemory(session.FilesystemID); err != nil {
		return c, fmt.Errorf("shared context: memory: %w", err)
	}
	if c.Categories, err = repo.GetCategoryUsageSummary(session.FilesystemID, session.ID); err != nil {
		return c, fmt.Errorf("shared context: categories: %w", err)
	}
	seen := map[string]bool{}
	var ancestors []string
	for _, u := range unitPaths {
		for _, a := range append([]string{strings.Trim(u, "/")}, sourceAncestors(u)...) {
			if a != "" && a != "." && !seen[a] {
				seen[a] = true
				ancestors = append(ancestors, a)
			}
		}
	}
	if c.ParentChain, err = repo.GetEntriesByPaths(session.ID, ancestors); err != nil {
		return c, fmt.Errorf("shared context: parent chain: %w", err)
	}
	return c, nil
}

// BuildSharedContext loads and renders the shared context block in one call.
func BuildSharedContext(repo *db.Repository, session *db.ScanSession, unitPaths []string, notes string) (string, error) {
	c, err := LoadSharedContext(repo, session, unitPaths, notes)
	if err != nil {
		return "", err
	}
	return RenderSharedContext(c), nil
}

func buildDirectoryPrompt(dir db.FileEntry, categorySummary string) string {
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
	sb.WriteString(categorySummary)
	return sb.String()
}

// buildHarnessPrompt is the continuous worker's kickoff message: loop
// pull → classify → mark → pull until the untagged queue is empty. The
// system auto-shards untagged pulls across workers and rolls a new timeline
// batch on each pull, so the agent only ever sees its own shard.
func buildHarnessPrompt(categorySummary string) string {
	return `持续处理本会话所有未打标的散文件，循环"拉取 → 归类 → 标记 → 再拉"，直到没有剩余：

1. 用 list_files(file_type="file", tagged=false, limit=200) 拉取下一批未打标文件（系统自动分片，你只会拿到属于本 worker 的文件；每拉一批时间线上就是一个新批次）
2. 逐个判断归类（规则同系统提示）：优先归入现有分类；压缩包参考文件名与归档摘要，需要内容时用 read_file 或 "路径::条目"；大批量同类文件优先用 run_script 一次脚本批量 set_category + mark_tagged
3. 每个处理过的文件都必须 mark_tagged（工具或脚本内建函数）——set_target/set_category 不会置 tagged，漏标会被系统记为失败
4. 回到第 1 步再拉；当 list_files 返回 files 为空（total=0）时结束，输出一段总结：新建了哪些分类、各归入多少、有何遗留与原因
` + categorySummary
}
func buildBatchPrompt(entries []db.FileEntry, categorySummary string, hints map[uint]string) string {
	var sb strings.Builder
	sb.WriteString("处理以下散文件：\n\n")
	for i, e := range entries {
		sb.WriteString(fmt.Sprintf("%d. 路径: %s\n   类型: %s | 大小: %d\n",
			i+1, e.OriginalPath, e.FileType, e.Size))
		if e.Description != "" {
			sb.WriteString(fmt.Sprintf("   描述: %s\n", e.Description))
		}
		if h := hints[e.ID]; h != "" {
			sb.WriteString(fmt.Sprintf("   内容摘要: %s\n", h))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(categorySummary)
	return sb.String()
}

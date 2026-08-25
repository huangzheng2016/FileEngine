package agent

import (
	"fmt"
	"sort"
	"strings"

	"FileEngine/internal/db"
)

// buildCategorySummary renders the complete category system for the agent
// prompt: every category is listed (used and unused alike) so the agent never
// plans against a partial view. Entries are sorted by usage descending then
// name ascending, keeping the most relevant categories at the top when the
// list is long. usages is sorted in place.
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
	}
	return sb.String()
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

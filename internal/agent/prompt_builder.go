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

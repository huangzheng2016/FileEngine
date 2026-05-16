package agent

import (
	"fmt"
	"strings"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func buildCategorySummary(usages []db.CategoryUsage) string {
	if len(usages) == 0 {
		return ""
	}
	maxCategories := config.Get().Agent.MaxCategorySummaryCount
	if maxCategories <= 0 {
		maxCategories = 20
	}
	if len(usages) > maxCategories {
		usages = usages[:maxCategories]
	}
	var sb strings.Builder
	sb.WriteString("\n## 当前分类使用情况\n")
	for _, u := range usages {
		sb.WriteString(fmt.Sprintf("- %s (%s): 已归入 %d 项\n", u.Name, u.Path, u.FileCount))
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

func buildBatchPrompt(entries []db.FileEntry, categorySummary string) string {
	var sb strings.Builder
	sb.WriteString("处理以下散文件：\n\n")
	for i, e := range entries {
		sb.WriteString(fmt.Sprintf("%d. 路径: %s\n   类型: %s | 大小: %d\n",
			i+1, e.OriginalPath, e.FileType, e.Size))
		if e.Description != "" {
			sb.WriteString(fmt.Sprintf("   描述: %s\n", e.Description))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(categorySummary)
	return sb.String()
}

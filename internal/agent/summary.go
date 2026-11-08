package agent

import (
	"context"
	"fmt"
	"strings"

	"FileEngine/internal/db"
)

// SummaryAgent generates a human-readable summary of operation results.
type SummaryAgent struct {
	repo      *db.Repository
	sessionID uint
}

func NewSummaryAgent(repo *db.Repository, sessionID uint) *SummaryAgent {
	return &SummaryAgent{repo: repo, sessionID: sessionID}
}

// Run generates a summary of the current session state.
func (a *SummaryAgent) Run(ctx context.Context) (string, error) {
	session, err := a.repo.GetSession(a.sessionID)
	if err != nil {
		return "", fmt.Errorf("get session: %w", err)
	}

	usages, err := a.repo.GetCategoryUsageSummary(session.FilesystemID, a.sessionID)
	if err != nil {
		return "", fmt.Errorf("get category usage: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 会话 %d 执行报告\n\n", a.sessionID))
	sb.WriteString(fmt.Sprintf("- 状态: %s\n", session.Status))
	sb.WriteString(fmt.Sprintf("- 总文件数: %d\n", session.TotalFiles))
	sb.WriteString(fmt.Sprintf("- 已标记: %d\n", session.TaggedFiles))
	sb.WriteString(fmt.Sprintf("- 计划操作: %d\n", session.PlannedOps))
	sb.WriteString(fmt.Sprintf("- 已执行: %d\n", session.ExecutedOps))
	sb.WriteString("\n")

	var used []db.CategoryUsage
	for _, u := range usages {
		if u.FileCount > 0 {
			used = append(used, u)
		}
	}
	if len(used) > 0 {
		sb.WriteString("## 分类统计\n\n")
		for _, u := range used {
			sb.WriteString(fmt.Sprintf("- **%s** (`%s`): %d 项\n", u.Name, u.Path, u.FileCount))
		}
	}

	return sb.String(), nil
}

// untaggedReconciliationSection renders the end-of-run reconciliation of
// entries still undecided, split by type (symlinks never take part), plus
// the junk entries skipped without an LLM call.
func untaggedReconciliationSection(b db.UntaggedBreakdown, ignored int64) string {
	miss := ""
	if b.Files+b.Directories > 0 {
		miss = "；不为 0 表示有遗漏，原因见失败与遗漏的任务"
	}
	junk := ""
	if ignored > 0 {
		junk = fmt.Sprintf("；系统垃圾已自动跳过 %d 项", ignored)
	}
	return fmt.Sprintf("\n## 决定对账\n\n- 未决定剩余：文件 %d / 目录 %d / 软链 %d（软链不参与整理%s%s）\n",
		b.Files, b.Directories, b.Symlinks, miss, junk)
}

// emptyCategoryPruneSection records the AI-created empty categories pruned at
// run end (created but never used). Empty input renders nothing.
func emptyCategoryPruneSection(pruned []string) string {
	if len(pruned) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n## 空分类清理（%d）\n\n", len(pruned)))
	for _, p := range pruned {
		sb.WriteString(fmt.Sprintf("- %s：AI 创建但零归入，已自动删除\n", p))
	}
	return sb.String()
}

// failureReportSection lists the work units that failed or left entries
// undecided (capped).
func failureReportSection(failures []BatchFailure) string {
	if len(failures) == 0 {
		return ""
	}
	const maxListed = 20
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n## 失败与遗漏的任务（%d）\n\n", len(failures)))
	for i, f := range failures {
		if i >= maxListed {
			sb.WriteString(fmt.Sprintf("- … 其余 %d 个从略\n", len(failures)-maxListed))
			break
		}
		sb.WriteString(fmt.Sprintf("- **%s**：%s\n", f.Target, f.Err))
	}
	return sb.String()
}

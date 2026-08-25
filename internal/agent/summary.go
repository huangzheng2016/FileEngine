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

// untaggedReconciliationSection renders the end-of-run tagging reconciliation:
// untagged remainders split by entry type. Untagged directories and symlinks
// are expected deltas — container directories are described but deliberately
// left untagged so their children can be classified on their own, and symlinks
// never participate in tagging. Only a non-zero file count signals real
// misses, which the failure section above then explains.
func untaggedReconciliationSection(b db.UntaggedBreakdown) string {
	miss := ""
	if b.Files > 0 {
		miss = "；未打标文件不为 0 表示存在漏标，原因见失败批次与日志"
	}
	return fmt.Sprintf("\n## 打标对账\n\n- 未打标剩余：文件 %d / 目录 %d / 软链 %d（容器目录与软链属正常差额：容器只描述不打标、软链不参与打标%s）\n",
		b.Files, b.Directories, b.Symlinks, miss)
}

// failureReportSection renders the failed-batch section appended to a tagging
// report: total count plus each batch's target and error. Long failure lists
// are capped so the report stays readable.
func failureReportSection(failures []BatchFailure) string {
	if len(failures) == 0 {
		return ""
	}
	const maxListed = 20
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n## 失败批次（%d）\n\n", len(failures)))
	for i, f := range failures {
		if i >= maxListed {
			sb.WriteString(fmt.Sprintf("- … 其余 %d 个批次从略，详见日志\n", len(failures)-maxListed))
			break
		}
		sb.WriteString(fmt.Sprintf("- 批次 %d（%s）: %s\n", f.BatchIndex, f.Target, f.Err))
	}
	return sb.String()
}

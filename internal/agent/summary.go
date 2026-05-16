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

	if len(usages) > 0 {
		sb.WriteString("## 分类统计\n\n")
		for _, u := range usages {
			sb.WriteString(fmt.Sprintf("- **%s** (`%s`): %d 项\n", u.Name, u.Path, u.FileCount))
		}
	}

	return sb.String(), nil
}

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/db"
)

const reflectionNotesMax = 200

func ParseMemoryOps(text string) ([]MemoryOp, error) {
	text = strings.TrimSpace(text)
	if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i >= 0 && j > i {
		var obj struct {
			Ops []MemoryOp `json:"ops"`
		}
		if err := json.Unmarshal([]byte(text[i:j+1]), &obj); err == nil {
			return obj.Ops, nil
		}
	}
	if i, j := strings.Index(text, "["), strings.LastIndex(text, "]"); i >= 0 && j > i {
		var ops []MemoryOp
		if err := json.Unmarshal([]byte(text[i:j+1]), &ops); err == nil {
			return ops, nil
		}
	}
	return nil, fmt.Errorf("记忆整理结果不是合法的 JSON：%.200s", text)
}

func buildReflectionInput(notes, longTerm []db.MemoryItem, session *db.ScanSession, tagging *db.AgentRun) string {
	var corrections []db.MemoryItem
	for _, m := range longTerm {
		if m.Source == db.MemorySourceCorrection && !m.UpdatedAt.Before(session.CreatedAt) {
			corrections = append(corrections, m)
		}
	}
	failures := ""
	if tagging != nil {
		failures = failureSections(tagging.Summary)
		if tagging.Error != "" {
			failures = "运行错误：" + tagging.Error + "\n\n" + failures
		}
	}
	if len(notes) == 0 && len(corrections) == 0 && strings.TrimSpace(failures) == "" {
		return ""
	}

	var sb strings.Builder
	if g := strings.TrimSpace(session.Goal); g != "" {
		sb.WriteString("## 用户目标\n\n" + g + "\n\n")
	}
	sb.WriteString("## 已有长期记忆\n\n")
	if len(longTerm) == 0 {
		sb.WriteString("（无）\n")
	}
	for _, m := range longTerm {
		owner := ""
		if m.Pinned || m.Source == db.MemorySourceUser {
			owner = "，用户维护"
		}
		sb.WriteString(fmt.Sprintf("- [id=%d，%s，%s%s] %s：%s", m.ID, m.Scope, m.Kind, owner, m.Key, m.Content))
		if m.Path != "" {
			sb.WriteString("（路径：" + SanitizeUntrustedName(m.Path) + "）")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	if len(corrections) > 0 {
		sb.WriteString("## 本会话期间用户做的纠正\n\n")
		for _, m := range corrections {
			sb.WriteString(fmt.Sprintf("- [id=%d] %s\n", m.ID, SanitizeUntrustedName(m.Content)))
		}
		sb.WriteString("\n")
	}
	if len(notes) > 0 {
		sb.WriteString("## 本会话的工作笔记\n\n")
		for _, n := range notes {
			sb.WriteString("- " + SanitizeUntrustedName(n.Content))
			if n.Path != "" {
				sb.WriteString("（" + SanitizeUntrustedName(n.Path) + "）")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	if strings.TrimSpace(failures) != "" {
		sb.WriteString("## 本次运行的失败与遗漏\n\n" + failures + "\n")
	}
	return sb.String()
}

func (a *Agent) Reflect(ctx context.Context, logger *Logger, tagging *db.AgentRun) (MemoryOpsResult, error) {
	notes, err := a.repo.NotesSince(a.sessionID, 0, reflectionNotesMax)
	if err != nil {
		return MemoryOpsResult{}, err
	}
	longTerm, err := a.repo.ListLongTermMemory(a.session.FilesystemID)
	if err != nil {
		return MemoryOpsResult{}, err
	}
	input := buildReflectionInput(notes, longTerm, a.session, tagging)
	if input == "" {
		return MemoryOpsResult{}, nil
	}
	m, err := newChatModel(ctx, a.modelProvider)
	if err != nil {
		return MemoryOpsResult{}, fmt.Errorf("create model: %w", err)
	}
	tracker := newTokenTrackingModel(m)
	defer func() { logger.AddUsage(tracker.Usage()) }()
	msg, err := tracker.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: reflectionPrompt},
		{Role: schema.User, Content: input},
	})
	if err != nil {
		return MemoryOpsResult{}, err
	}
	ops, err := ParseMemoryOps(msg.Content)
	if err != nil {
		return MemoryOpsResult{}, err
	}
	res, err := ApplyMemoryOps(a.repo, a.session.FilesystemID, ops)
	for _, c := range res.Changed {
		db.Emit(a.sessionID, logger.RunID(), db.EvMemoryUpdated, db.MemoryUpdatedData{RunID: logger.RunID(), Scope: c.Scope, ID: c.ID})
	}
	if err != nil {
		return res, err
	}
	line := fmt.Sprintf("记忆整理：新增 %d，修改 %d，删除 %d", res.Added, res.Updated, res.Deleted)
	if len(res.Rejected) > 0 {
		line += "；跳过 " + strings.Join(res.Rejected, "；")
	}
	logger.LogMessage("system", line)
	return res, nil
}

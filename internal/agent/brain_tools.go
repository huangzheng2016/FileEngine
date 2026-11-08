package agent

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"FileEngine/internal/db"
)

// planMaxRunes caps a plan written by the agent.
const planMaxRunes = 20000

type WritePlanInput struct {
	Content string `json:"content" jsonschema_description:"完整的整理方案（Markdown，中文）：分类体系（每类的路径、放什么、命名规则）、路径约定、特殊处理（项目整体保留、垃圾、重复）、待用户确认的问题。整体替换旧方案"`
}

type WritePlanOutput struct {
	Success bool   `json:"success"`
	Version int    `json:"version,omitempty"`
	Status  string `json:"status,omitempty"`
	Error   string `json:"error,omitempty"`
}

type ReadPlanInput struct{}

type ReadPlanOutput struct {
	Exists  bool   `json:"exists"`
	Version int    `json:"version"`
	Status  string `json:"status,omitempty"`
	Content string `json:"content,omitempty"`
}

func (tb *ToolBuilder) writePlan(ctx context.Context, in *WritePlanInput) (*WritePlanOutput, error) {
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return &WritePlanOutput{Error: "方案内容不能为空"}, nil
	}
	if n := utf8.RuneCountInString(content); n > planMaxRunes {
		return &WritePlanOutput{Error: fmt.Sprintf("方案有 %d 字，超过 %d 字上限：请精简（列规则和约定，不要逐个列文件）", n, planMaxRunes)}, nil
	}
	// Agent edits always go back to draft: the user approves the plan.
	plan, err := tb.repo.SavePlan(tb.sessionID, content, -1, db.PlanDraft)
	if err != nil {
		return nil, fmt.Errorf("write plan: %w", err)
	}
	db.Emit(tb.sessionID, tb.RunID, db.EvPlanUpdated, db.PlanUpdatedData{RunID: tb.RunID, Version: plan.Version, Status: plan.Status})
	return &WritePlanOutput{Success: true, Version: plan.Version, Status: plan.Status}, nil
}

func (tb *ToolBuilder) readPlan(ctx context.Context, in *ReadPlanInput) (*ReadPlanOutput, error) {
	plan, err := tb.repo.GetPlan(tb.sessionID)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	return &ReadPlanOutput{Exists: plan.Version > 0, Version: plan.Version, Status: plan.Status, Content: plan.Content}, nil
}

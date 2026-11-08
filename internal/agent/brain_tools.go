package agent

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"FileEngine/internal/db"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
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

// buildBrainTools returns the shared-context tools: read_plan, note,
// remember, recall — plus write_plan when withPlanWrite (the session
// assistant and the survey write the plan; parallel tagging workers only read
// it).
func (tb *ToolBuilder) buildBrainTools(withPlanWrite bool) ([]tool.BaseTool, error) {
	type spec struct {
		name, desc string
		build      func(name, desc string) (tool.BaseTool, error)
	}
	specs := []spec{
		{"read_plan", "读取本会话的整理方案（Markdown）。exists=false 表示还没有方案", func(n, d string) (tool.BaseTool, error) { return utils.InferTool(n, d, tb.readPlan) }},
		{"note", "写一条本会话共享笔记，其他并行 worker 处理下一个单元前会看到。用于其他人也需要知道的发现：项目根目录、命名风格、某目录的真实用途、你定下的归类约定。不要记流水账", func(n, d string) (tool.BaseTool, error) { return utils.InferTool(n, d, tb.note) }},
		{"remember", "写一条长期记忆（跨会话保留）：用户明确表达的长期偏好（scope=global），或此文件系统的命名/归类约定（scope=filesystem）。同一 key 覆盖更新。只记稳定、可复用的规则", func(n, d string) (tool.BaseTool, error) { return utils.InferTool(n, d, tb.remember) }},
		{"recall", "查询记忆（用户偏好、此文件系统的约定、历史纠正、本会话笔记）。拿不准某类文件放哪时先查", func(n, d string) (tool.BaseTool, error) { return utils.InferTool(n, d, tb.recall) }},
	}
	if withPlanWrite {
		specs = append(specs, spec{"write_plan", "写入/更新本会话的整理方案（整体替换，保存为待确认草稿）。方案是所有 worker 共同遵守的规范", func(n, d string) (tool.BaseTool, error) { return utils.InferTool(n, d, tb.writePlan) }})
	}
	tools := make([]tool.BaseTool, 0, len(specs))
	for _, s := range specs {
		t, err := s.build(s.name, s.desc)
		if err != nil {
			return nil, fmt.Errorf("build %s: %w", s.name, err)
		}
		tools = append(tools, t)
	}
	return tools, nil
}

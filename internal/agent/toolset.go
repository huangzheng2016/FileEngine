package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ToolsetKind selects the tool set of a run kind. Each kind has one fixed,
// deterministically ordered tool list per session (cache affinity: tools are
// the first part of every request prefix).
type ToolsetKind int

const (
	ToolsetTagging ToolsetKind = iota
	ToolsetChat
	ToolsetSurvey
	ToolsetReview
)

type toolSpec struct {
	name, desc string
	build      func(name, desc string) (tool.BaseTool, error)
	when       bool
}

func infer[I, O any](fn func(context.Context, *I) (*O, error)) func(string, string) (tool.BaseTool, error) {
	return func(n, d string) (tool.BaseTool, error) { return utils.InferTool(n, d, fn) }
}

// BuildToolset returns the tools of a run kind in a fixed order. Session
// switches (read_file, create_category, view_image, run_script) are decided
// once here, never per unit.
func (tb *ToolBuilder) BuildToolset(kind ToolsetKind) ([]tool.BaseTool, error) {
	s := tb.session
	specs := []toolSpec{
		{"list_files", "列出目录子项（TSV：名称/类型/大小/角色/目标/描述），默认 100 条，用 next_offset 翻页", infer(tb.listFiles), true},
		{"get_file_info", "单个条目的详细信息", infer(tb.getFileInfo), true},
		{"read_file", "读文件内容（默认 8000 字，用 offset 续读）；PDF/Office 提取文本；zip/tar 列目录，路径::条目 读包内文本；敏感文件不读", infer(tb.readFileUntrusted), s.AllowReadFile},
		{"set_targets", "批量给文件设目标路径（≤200 条，每条带 reason，可带 confidence）；new_path 为空表示撤销决定", infer(tb.setTargets), true},
		{"decide_dirs", "批量决定目录：unit 整体移动 / container 逐项处理 / skip 保持原位（≤200 条，带 reason）；也可对文件用 skip", infer(tb.decideDirs), true},
		{"split_unit", "把整体移动（或整体跳过）的目录拆回逐项处理", infer(tb.splitUnit), true},
		{"update_description", "给条目写一句描述", infer(tb.updateDescription), true},
		{"list_categories", "列出分类（提示里已有完整列表时不必调用）", infer(tb.listCategories), true},
		{"create_category", "没有合适分类时新建（先看现有分类；重名会被拒绝）" + categoryPolicySuffix(tb.cfg.CategoryPolicy), infer(tb.createCategory), s.AllowAutoCategory},
		{"update_category", "修改分类名称/路径/说明；路径变更会级联更新已规划目标", infer(tb.updateCategory), true},
		{"delete_category", "删除分类并撤销其下的规划", infer(tb.deleteCategory), true},
		{"add_rule", "新增整理规则（按扩展名/文件名正则/源目录/大小匹配，目标模板含 {year} {month} {taken_year} {camera} 等变量）；大量同类文件优先用规则", infer(tb.addRule), true},
		{"update_rule", "修改规则（只改给出的字段）", infer(tb.updateRule), true},
		{"delete_rule", "删除规则", infer(tb.deleteRule), true},
		{"apply_rules", "把规则应用到尚未决定的条目；preview=true 只看匹配数和样例", infer(tb.applyRules), true},
		{"read_plan", "读取整理方案全文", infer(tb.readPlan), true},
		{"write_plan", "写入/更新整理方案（整体替换，保存为待确认草稿）", infer(tb.writePlan), kind != ToolsetTagging},
		{"note", "写一条本会话共享笔记，其他 worker 会看到（项目根、命名风格、某目录的真实用途）", infer(tb.note), true},
		{"remember", "写长期记忆：用户的通用偏好（global）或此文件系统的约定（filesystem）", infer(tb.remember), true},
		{"recall", "查询记忆（偏好、约定、历史纠正、本会话笔记）", infer(tb.recall), true},
	}
	var tools []tool.BaseTool
	for _, sp := range specs {
		if !sp.when {
			continue
		}
		t, err := sp.build(sp.name, sp.desc)
		if err != nil {
			return nil, fmt.Errorf("build %s: %w", sp.name, err)
		}
		tools = append(tools, t)
	}
	if t, err := tb.buildVisionTool(); err != nil {
		return nil, err
	} else if t != nil {
		tools = append(tools, t)
	}
	if t, err := tb.buildScriptTool(); err != nil {
		return nil, err
	} else if t != nil {
		tools = append(tools, t)
	}
	return tools, nil
}

// BuildTools is the tagging worker tool set.
func (tb *ToolBuilder) BuildTools() ([]tool.BaseTool, error) { return tb.BuildToolset(ToolsetTagging) }

// BuildInstructTools is the session assistant tool set.
func (tb *ToolBuilder) BuildInstructTools() ([]tool.BaseTool, error) {
	return tb.BuildToolset(ToolsetChat)
}

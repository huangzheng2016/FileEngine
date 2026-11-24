package agent

import (
	"context"
	"fmt"
	"strings"

	"FileEngine/internal/db"
)

type AddRuleInput struct {
	Name       string `json:"name" jsonschema_description:"规则名：中文，说明用途，如 照片按年份；在本会话内唯一，之后用它修改或删除规则"`
	Exts       string `json:"exts,omitempty" jsonschema_description:"扩展名，逗号分隔、不带点，如 jpg,heic"`
	NameRegex  string `json:"name_regex,omitempty" jsonschema_description:"文件名正则（Go RE2 语法），匹配文件名而不是路径"`
	PathPrefix string `json:"path_prefix,omitempty" jsonschema_description:"只匹配这个源目录（含子目录）下的条目；整理运行中必填"`
	FileType   string `json:"file_type,omitempty" jsonschema:"enum=file,enum=directory" jsonschema_description:"只匹配文件或目录；不填两者都匹配"`
	MinSize    int64  `json:"min_size,omitempty" jsonschema_description:"最小字节数，0 表示不限"`
	MaxSize    int64  `json:"max_size,omitempty" jsonschema_description:"最大字节数，0 表示不限"`
	Target     string `json:"target" jsonschema_description:"目标路径模板，如 /照片/{year}/{month}。变量：{name} 文件名，{stem} 不含扩展名的文件名，{ext} 扩展名，{year} {month} {day} 拍摄日期（没有时用修改日期），{taken_year} {taken_month} 拍摄日期（没有时为 未知日期），{camera} 相机型号，{parent} 源目录名，{rel} 相对 path_prefix 的路径。不含 {name}、{stem}、{rel} 时自动追加文件名"`
	Role       string `json:"role,omitempty" jsonschema:"enum=file,enum=unit" jsonschema_description:"file（默认）；unit 表示匹配到的目录整体移动"`
	Position   int    `json:"position,omitempty" jsonschema_description:"顺序，小的先匹配；不填追加到最后"`
}

type AddRuleOutput struct {
	Envelope
	Rule           string `json:"rule,omitempty"`
	MatchedPreview int    `json:"matched_preview"`
}

type UpdateRuleInput struct {
	Name       string   `json:"name" jsonschema_description:"要修改的规则名"`
	NewName    string   `json:"new_name,omitempty" jsonschema_description:"新规则名"`
	Exts       string   `json:"exts,omitempty" jsonschema_description:"扩展名，逗号分隔、不带点"`
	NameRegex  string   `json:"name_regex,omitempty" jsonschema_description:"文件名正则（Go RE2 语法）"`
	PathPrefix string   `json:"path_prefix,omitempty" jsonschema_description:"只匹配这个源目录（含子目录）下的条目"`
	FileType   string   `json:"file_type,omitempty" jsonschema:"enum=file,enum=directory" jsonschema_description:"只匹配文件或目录"`
	MinSize    int64    `json:"min_size,omitempty" jsonschema_description:"最小字节数"`
	MaxSize    int64    `json:"max_size,omitempty" jsonschema_description:"最大字节数"`
	Target     string   `json:"target,omitempty" jsonschema_description:"目标路径模板，如 /照片/{year}/{month}。变量：{name} 文件名，{stem} 不含扩展名的文件名，{ext} 扩展名，{year} {month} {day} 拍摄日期（没有时用修改日期），{taken_year} {taken_month} 拍摄日期（没有时为 未知日期），{camera} 相机型号，{parent} 源目录名，{rel} 相对 path_prefix 的路径。不含 {name}、{stem}、{rel} 时自动追加文件名"`
	Role       string   `json:"role,omitempty" jsonschema:"enum=file,enum=unit" jsonschema_description:"file 或 unit"`
	Position   int      `json:"position,omitempty" jsonschema_description:"顺序，小的先匹配"`
	Clear      []string `json:"clear,omitempty" jsonschema:"enum=exts,enum=name_regex,enum=path_prefix,enum=file_type,enum=min_size,enum=max_size" jsonschema_description:"要清除（恢复为不限）的条件字段；没给出的字段保持不变"`
}

type DeleteRuleInput struct {
	Name string `json:"name" jsonschema_description:"要删除的规则名"`
}

type RuleOutput struct {
	Envelope
	Rule string `json:"rule,omitempty"`
}

type ApplyRulesInput struct {
	Rule    string `json:"rule,omitempty" jsonschema_description:"只处理这条规则（规则名）；不填处理全部"`
	Preview bool   `json:"preview,omitempty" jsonschema_description:"true 时只返回匹配数和样例，不写入"`
}

type RuleSampleView struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

type RuleStatView struct {
	Name    string           `json:"name"`
	Matched int              `json:"matched"`
	Skipped int              `json:"skipped,omitempty"`
	Samples []RuleSampleView `json:"samples,omitempty"`
}

type ApplyRulesOutput struct {
	Envelope
	Rules []RuleStatView `json:"rules"`
}

func (in *AddRuleInput) toRule(sessionID uint) *db.PlanRule {
	return &db.PlanRule{SessionID: sessionID, Name: in.Name, Exts: in.Exts, NameRegex: in.NameRegex, PathPrefix: in.PathPrefix,
		FileType: in.FileType, MinSize: in.MinSize, MaxSize: in.MaxSize, Target: in.Target, Role: in.Role, Position: in.Position,
		Source: db.MemorySourceAgent}
}

func ruleTargetPrefix(target string) string {
	if i := strings.IndexByte(target, '{'); i >= 0 {
		target = target[:i]
		if j := strings.LastIndexByte(target, '/'); j >= 0 {
			target = target[:j]
		} else {
			target = ""
		}
	}
	return db.NormalizeTargetPath(target)
}

// checkRuleTarget returns a model-fixable message, or "".
func (tb *ToolBuilder) checkRuleTarget(target string) (string, error) {
	checker, err := tb.newTargetChecker()
	if err != nil {
		return "", err
	}
	prefix := ruleTargetPrefix(target)
	if prefix == "" {
		if checker.hasPlan {
			return "", nil
		}
		return "目标模板必须以分类路径开头（如 /照片/{year}）", nil
	}
	return checker.check(prefix + "/x"), nil
}

// ruleByName resolves a rule of this session; names are the model-facing key.
func (tb *ToolBuilder) ruleByName(name string) (*db.PlanRule, error) {
	name = strings.TrimSpace(name)
	rules, err := tb.repo.ListRules(tb.sessionID)
	if err != nil {
		return nil, err
	}
	var found *db.PlanRule
	for i := range rules {
		if strings.TrimSpace(rules[i].Name) != name {
			continue
		}
		if found != nil {
			return nil, rejectf("有多条同名规则「%s」，无法确定是哪一条", SanitizeUntrustedName(name))
		}
		found = &rules[i]
	}
	if found == nil {
		return nil, rejectHint("规则名见共享上下文里的规则列表", "规则不存在：%s", SanitizeUntrustedName(name))
	}
	return found, nil
}

func (tb *ToolBuilder) addRule(ctx context.Context, in *AddRuleInput) (*AddRuleOutput, error) {
	if tb.RunKind == db.RunPhaseTagging && strings.Trim(strings.TrimSpace(in.PathPrefix), "/") == "" {
		return failed[AddRuleOutput]("整理运行中规则必须带 path_prefix",
			"规则作用于整个会话的未决定条目；用 path_prefix 限定到当前任务目录，只涉及当前目录的批量决定用 decide 或 run_script"), nil
	}
	if msg, err := tb.checkRuleTarget(in.Target); err != nil {
		return nil, err
	} else if msg != "" {
		return failed[AddRuleOutput](msg), nil
	}
	rules, err := tb.repo.ListRules(tb.sessionID)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if strings.TrimSpace(r.Name) == strings.TrimSpace(in.Name) {
			return failed[AddRuleOutput](fmt.Sprintf("已有同名规则「%s」", SanitizeUntrustedName(in.Name)), "换一个规则名，或用 update_rule 修改它"), nil
		}
	}
	rule := in.toRule(tb.sessionID)
	if err := tb.repo.CreateRule(rule); err != nil {
		return nil, err
	}
	out := succeeded[AddRuleOutput]()
	out.Rule = rule.Name
	stats, err := tb.repo.ApplyRules(tb.sessionID, rule.ID, true)
	if err != nil {
		return nil, err
	}
	for _, st := range stats {
		out.MatchedPreview += st.Matched
	}
	out.Hint = "规则已保存，还没有写入决定；apply_rules 才会应用"
	return out, nil
}

func (tb *ToolBuilder) updateRule(ctx context.Context, in *UpdateRuleInput) (*RuleOutput, error) {
	rule, err := tb.ruleByName(in.Name)
	if err != nil {
		return nil, err
	}
	if in.Target != "" {
		if msg, err := tb.checkRuleTarget(in.Target); err != nil {
			return nil, err
		} else if msg != "" {
			return failed[RuleOutput](msg), nil
		}
	}
	clear := map[string]bool{}
	for _, f := range in.Clear {
		clear[f] = true
	}
	if newName := strings.TrimSpace(in.NewName); newName != "" && newName != rule.Name {
		if other, err := tb.ruleByName(newName); err == nil && other != nil {
			return failed[RuleOutput](fmt.Sprintf("已有同名规则「%s」", SanitizeUntrustedName(newName))), nil
		}
	}
	updated, err := tb.repo.UpdateRule(rule.ID, func(r *db.PlanRule) {
		if in.NewName != "" {
			r.Name = in.NewName
		}
		if in.Exts != "" {
			r.Exts = in.Exts
		}
		if in.NameRegex != "" {
			r.NameRegex = in.NameRegex
		}
		if in.PathPrefix != "" {
			r.PathPrefix = in.PathPrefix
		}
		if in.FileType != "" {
			r.FileType = in.FileType
		}
		if in.MinSize != 0 {
			r.MinSize = in.MinSize
		}
		if in.MaxSize != 0 {
			r.MaxSize = in.MaxSize
		}
		if in.Target != "" {
			r.Target = in.Target
		}
		if in.Role != "" {
			r.Role = in.Role
		}
		if in.Position != 0 {
			r.Position = in.Position
		}
		if clear["exts"] {
			r.Exts = ""
		}
		if clear["name_regex"] {
			r.NameRegex = ""
		}
		if clear["path_prefix"] {
			r.PathPrefix = ""
		}
		if clear["file_type"] {
			r.FileType = ""
		}
		if clear["min_size"] {
			r.MinSize = 0
		}
		if clear["max_size"] {
			r.MaxSize = 0
		}
	})
	if err != nil {
		return nil, err
	}
	out := succeeded[RuleOutput]()
	out.Rule = updated.Name
	return out, nil
}

func (tb *ToolBuilder) deleteRule(ctx context.Context, in *DeleteRuleInput) (*RuleOutput, error) {
	rule, err := tb.ruleByName(in.Name)
	if err != nil {
		return nil, err
	}
	if err := tb.repo.DeleteRule(rule.ID); err != nil {
		if err == db.ErrRuleNotFound {
			return nil, rejectf("规则不存在：%s", SanitizeUntrustedName(in.Name))
		}
		return nil, err
	}
	out := succeeded[RuleOutput]()
	out.Rule = rule.Name
	out.Hint = "已经按它写入的决定保持不变"
	return out, nil
}

func (tb *ToolBuilder) applyRules(ctx context.Context, in *ApplyRulesInput) (*ApplyRulesOutput, error) {
	if msg := tb.decidingGate(); msg != "" {
		return failed[ApplyRulesOutput](msg), nil
	}
	var ruleID uint
	if strings.TrimSpace(in.Rule) != "" {
		r, err := tb.ruleByName(in.Rule)
		if err != nil {
			return nil, err
		}
		ruleID = r.ID
	}
	stats, err := tb.repo.ApplyRules(tb.sessionID, ruleID, in.Preview)
	if err != nil {
		return nil, err
	}
	out := succeeded[ApplyRulesOutput]()
	out.Rules = make([]RuleStatView, 0, len(stats))
	for _, st := range stats {
		v := RuleStatView{Name: SanitizeUntrustedName(st.Name), Matched: st.Matched, Skipped: st.Skipped}
		for _, s := range st.Samples {
			if len(v.Samples) == 3 {
				break
			}
			v.Samples = append(v.Samples, RuleSampleView{Path: SanitizeUntrustedName(s.Path), Target: SanitizeUntrustedName(s.Target)})
		}
		out.Rules = append(out.Rules, v)
	}
	return out, nil
}

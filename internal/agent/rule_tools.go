package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"FileEngine/internal/db"
)

type RuleInput struct {
	Name       string `json:"name" jsonschema_description:"规则名（中文，说明用途）"`
	Exts       string `json:"exts,omitempty" jsonschema_description:"扩展名，逗号分隔，如 jpg,heic"`
	NameRegex  string `json:"name_regex,omitempty" jsonschema_description:"文件名正则（Go 语法）"`
	PathPrefix string `json:"path_prefix,omitempty" jsonschema_description:"只匹配该源目录下的条目"`
	FileType   string `json:"file_type,omitempty" jsonschema_description:"file / directory，空=都可"`
	MinSize    int64  `json:"min_size,omitempty"`
	MaxSize    int64  `json:"max_size,omitempty"`
	Target     string `json:"target" jsonschema_description:"目标模板，如 照片/{year}/{month}；变量 {name} {stem} {ext} {year} {month} {day} {parent} {rel} {taken_year} {taken_month} {camera}；不含 {name}/{stem}/{rel} 时自动追加文件名"`
	Role       string `json:"role,omitempty" jsonschema_description:"file（默认）或 unit（目录整体移动）"`
	Position   int    `json:"position,omitempty" jsonschema_description:"顺序，小的先匹配；0=追加到最后"`
}

type UpdateRuleInput struct {
	ID uint `json:"id"`
	RuleInput
}

type RuleIDInput struct {
	ID uint `json:"id"`
}

type ApplyRulesInput struct {
	Preview bool `json:"preview,omitempty" jsonschema_description:"true=只预览匹配数和样例，不写入"`
	RuleID  uint `json:"rule_id,omitempty" jsonschema_description:"只应用这一条规则；0=全部"`
}

type RuleOutput struct {
	Success bool   `json:"success"`
	ID      uint   `json:"id,omitempty"`
	Error   string `json:"error,omitempty"`
}

type ApplyRulesOutput struct {
	Success bool               `json:"success"`
	Rules   []db.RuleApplyStat `json:"rules,omitempty"`
	Error   string             `json:"error,omitempty"`
}

func (in *RuleInput) toRule(sessionID uint) *db.PlanRule {
	return &db.PlanRule{SessionID: sessionID, Name: in.Name, Exts: in.Exts, NameRegex: in.NameRegex, PathPrefix: in.PathPrefix,
		FileType: in.FileType, MinSize: in.MinSize, MaxSize: in.MaxSize, Target: in.Target, Role: in.Role, Position: in.Position,
		Source: db.MemorySourceAgent}
}

// ruleTargetPrefix is the static part of a target template before the first
// variable, "/"-rooted.
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

// checkRuleTarget applies the set_target category rule to a template's static
// prefix: agent rules must land under a category unless a plan exists.
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
		return "目标模板必须以分类路径开头（如 照片/{year}）", nil
	}
	return checker.check(prefix + "/x"), nil
}

func ruleResult(id uint, err error) (*RuleOutput, error) {
	if err == nil {
		return &RuleOutput{Success: true, ID: id}, nil
	}
	if db.IsRejection(err) || errors.Is(err, db.ErrRuleNotFound) {
		return &RuleOutput{Error: err.Error()}, nil
	}
	return nil, err
}

func (tb *ToolBuilder) addRule(ctx context.Context, in *RuleInput) (*RuleOutput, error) {
	if msg, err := tb.checkRuleTarget(in.Target); err != nil || msg != "" {
		if err != nil {
			return nil, err
		}
		return &RuleOutput{Error: msg}, nil
	}
	rule := in.toRule(tb.sessionID)
	err := tb.repo.CreateRule(rule)
	return ruleResult(rule.ID, err)
}

func (tb *ToolBuilder) updateRule(ctx context.Context, in *UpdateRuleInput) (*RuleOutput, error) {
	rule, err := tb.repo.GetRule(in.ID)
	if err != nil || rule.SessionID != tb.sessionID {
		return &RuleOutput{Error: fmt.Sprintf("规则 %d 不存在", in.ID)}, nil
	}
	if in.Target != "" {
		if msg, err := tb.checkRuleTarget(in.Target); err != nil || msg != "" {
			if err != nil {
				return nil, err
			}
			return &RuleOutput{Error: msg}, nil
		}
	}
	_, err = tb.repo.UpdateRule(in.ID, func(r *db.PlanRule) {
		if in.Name != "" {
			r.Name = in.Name
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
	})
	return ruleResult(in.ID, err)
}

func (tb *ToolBuilder) deleteRule(ctx context.Context, in *RuleIDInput) (*RuleOutput, error) {
	rule, err := tb.repo.GetRule(in.ID)
	if err != nil || rule.SessionID != tb.sessionID {
		return &RuleOutput{Error: fmt.Sprintf("规则 %d 不存在", in.ID)}, nil
	}
	return ruleResult(in.ID, tb.repo.DeleteRule(in.ID))
}

func (tb *ToolBuilder) applyRules(ctx context.Context, in *ApplyRulesInput) (*ApplyRulesOutput, error) {
	stats, err := tb.repo.ApplyRules(tb.sessionID, in.RuleID, in.Preview)
	if err != nil {
		if db.IsRejection(err) {
			return &ApplyRulesOutput{Error: err.Error()}, nil
		}
		return nil, err
	}
	for i := range stats {
		if len(stats[i].Samples) > 3 {
			stats[i].Samples = stats[i].Samples[:3]
		}
	}
	return &ApplyRulesOutput{Success: true, Rules: stats}, nil
}

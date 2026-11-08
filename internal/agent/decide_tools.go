package agent

import (
	"context"
	"fmt"
	"strings"

	"FileEngine/internal/db"
	"FileEngine/internal/pathutil"
)

// maxBatchItems caps one set_targets / decide_dirs call.
const maxBatchItems = 200

type TargetItem struct {
	Path       string  `json:"path" jsonschema_description:"原路径"`
	NewPath    string  `json:"new_path" jsonschema_description:"目标路径（分类路径/子目录/文件名）"`
	Reason     string  `json:"reason" jsonschema_description:"一句话理由"`
	Confidence float64 `json:"confidence,omitempty" jsonschema_description:"把握 0-1；低于 0.6 会交给用户确认"`
}

type SetTargetsInput struct {
	Items []TargetItem `json:"items" jsonschema_description:"最多 200 条"`
}

type DecideItem struct {
	Path       string  `json:"path" jsonschema_description:"原路径"`
	Role       string  `json:"role" jsonschema_description:"unit=整体移动（需 new_path）；container=不移动，子项逐个处理；skip=保持原位（目录连同子项）"`
	NewPath    string  `json:"new_path,omitempty" jsonschema_description:"unit 的目标路径"`
	Reason     string  `json:"reason" jsonschema_description:"一句话理由"`
	Confidence float64 `json:"confidence,omitempty" jsonschema_description:"把握 0-1；低于 0.6 会交给用户确认"`
	Override   bool    `json:"override,omitempty" jsonschema_description:"目录下已有单独决定时，确认整体覆盖"`
}

type DecideDirsInput struct {
	Items []DecideItem `json:"items" jsonschema_description:"最多 200 条"`
}

type BatchFailed struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type BatchRenamed struct {
	Path    string `json:"path"`
	NewPath string `json:"new_path"`
}

// BatchOutput is the compact result of set_targets / decide_dirs.
type BatchOutput struct {
	Success     bool           `json:"success"`
	OK          int            `json:"ok"`
	Failed      []BatchFailed  `json:"failed,omitempty"`
	Renamed     []BatchRenamed `json:"renamed,omitempty"`
	NeedsReview int            `json:"needs_review,omitempty"`
	Overridden  []string       `json:"overridden,omitempty"`
	Error       string         `json:"error,omitempty"`
}

type SplitUnitInput struct {
	Path string `json:"path" jsonschema_description:"整体移动或整体跳过的目录"`
}

type SplitUnitOutput struct {
	Success  bool   `json:"success"`
	Released int64  `json:"released,omitempty"`
	Error    string `json:"error,omitempty"`
}

// targetChecker validates targets against the category system once per
// batch: a target must lie under a category path unless the session has a
// plan.
type targetChecker struct {
	cats    []db.Category
	hasPlan bool
}

func (tb *ToolBuilder) newTargetChecker() (*targetChecker, error) {
	cats, err := tb.repo.ListCategories(tb.filesystemID)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	hasPlan, err := tb.repo.HasPlan(tb.sessionID)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	return &targetChecker{cats: cats, hasPlan: hasPlan}, nil
}

func (c *targetChecker) check(newPath string) string {
	if err := pathutil.ValidateFSPath(newPath); err != nil {
		return fmt.Sprintf("invalid new_path: %v", err)
	}
	np := db.NormalizeTargetPath(newPath)
	for _, cat := range c.cats {
		if pathUnder(np, db.NormalizeTargetPath(cat.Path)) {
			return ""
		}
	}
	if c.hasPlan {
		return ""
	}
	if len(c.cats) == 0 {
		return fmt.Sprintf("目标 %s 不在任何分类路径下，而当前还没有任何分类：请先 create_category 或让用户在方案中定义目标结构", np)
	}
	paths := make([]string, len(c.cats))
	for i, cat := range c.cats {
		paths[i] = fmt.Sprintf("%s (%s)", cat.Name, db.NormalizeTargetPath(cat.Path))
	}
	return fmt.Sprintf("目标 %s 不在任何分类路径下，未写入。现有分类：%s", np, strings.Join(paths, "、"))
}

// pathUnder reports whether p equals dir or lies below it ("/"-rooted paths).
func pathUnder(p, dir string) bool {
	if dir == "/" {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// decide is the shared core of every decision tool and script builtin:
// category check, then the repository's invariant-enforcing DecideBatch.
// Rejections land in the output; only storage failures return an error.
func (tb *ToolBuilder) decide(ds []db.Decision) (*BatchOutput, []db.DecideOutcome, error) {
	out := &BatchOutput{}
	if len(ds) == 0 {
		out.Error = "items 不能为空"
		return out, nil, nil
	}
	if len(ds) > maxBatchItems {
		out.Error = fmt.Sprintf("一次最多 %d 条，本次 %d 条：请分批，或用规则（add_rule）处理大量同类文件", maxBatchItems, len(ds))
		return out, nil, nil
	}
	checker, err := tb.newTargetChecker()
	if err != nil {
		return nil, nil, err
	}
	var valid []db.Decision
	var idx []int
	outcomes := make([]db.DecideOutcome, len(ds))
	for i, d := range ds {
		d.Path = normalizePath(d.Path)
		if strings.TrimSpace(d.Reason) == "" {
			d.Reason = "AI 判断"
		}
		if (d.Role == db.RoleUnit || d.Role == db.RoleFile) && d.NewPath != "" {
			if msg := checker.check(d.NewPath); msg != "" {
				outcomes[i] = db.DecideOutcome{Err: &db.Rejection{Msg: msg}}
				continue
			}
		}
		valid = append(valid, d)
		idx = append(idx, i)
	}
	res, err := tb.repo.DecideBatch(tb.sessionID, valid)
	if err != nil {
		return nil, nil, err
	}
	for j, o := range res {
		outcomes[idx[j]] = o
	}
	for i, o := range outcomes {
		if o.Err != nil {
			out.Failed = append(out.Failed, BatchFailed{Path: ds[i].Path, Error: o.Err.Error()})
			continue
		}
		out.OK++
		if o.Result.Renamed {
			out.Renamed = append(out.Renamed, BatchRenamed{Path: o.Result.Path, NewPath: o.Result.NewPath})
		}
		if o.Result.NeedsReview {
			out.NeedsReview++
		}
		out.Overridden = append(out.Overridden, o.Result.Overridden...)
	}
	out.Success = len(out.Failed) == 0
	return out, outcomes, nil
}

func (tb *ToolBuilder) setTargets(ctx context.Context, in *SetTargetsInput) (*BatchOutput, error) {
	ds := make([]db.Decision, len(in.Items))
	for i, it := range in.Items {
		role := db.RoleFile
		if it.NewPath == "" {
			role = db.RoleUndecided
		}
		ds[i] = db.Decision{Path: it.Path, Role: role, NewPath: it.NewPath, Reason: it.Reason, Confidence: it.Confidence}
	}
	out, _, err := tb.decide(ds)
	return out, err
}

func (tb *ToolBuilder) decideDirs(ctx context.Context, in *DecideDirsInput) (*BatchOutput, error) {
	ds := make([]db.Decision, len(in.Items))
	for i, it := range in.Items {
		ds[i] = db.Decision{Path: it.Path, Role: it.Role, NewPath: it.NewPath, Reason: it.Reason, Confidence: it.Confidence, Override: it.Override}
	}
	out, _, err := tb.decide(ds)
	return out, err
}

func (tb *ToolBuilder) splitUnit(ctx context.Context, in *SplitUnitInput) (*SplitUnitOutput, error) {
	n, err := tb.repo.SplitUnit(tb.sessionID, in.Path)
	if err != nil {
		if db.IsRejection(err) {
			return &SplitUnitOutput{Error: err.Error()}, nil
		}
		return nil, err
	}
	return &SplitUnitOutput{Success: true, Released: n}, nil
}

// decideOne is the single-decision path used by script builtins.
func (tb *ToolBuilder) decideOne(d db.Decision) (*db.DecideResult, error) {
	out, outcomes, err := tb.decide([]db.Decision{d})
	if err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, &db.Rejection{Msg: out.Error}
	}
	if outcomes[0].Err != nil {
		return nil, outcomes[0].Err
	}
	return outcomes[0].Result, nil
}

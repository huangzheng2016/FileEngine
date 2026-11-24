package agent

import (
	"context"
	"fmt"
	"strings"

	"FileEngine/internal/db"
	"FileEngine/internal/pathutil"
)

const maxBatchItems = 200

const (
	actionMove      = "move"
	actionUnit      = "unit"
	actionContainer = "container"
	actionSkip      = "skip"
	actionUndo      = "undo"
)

type DecideItem struct {
	Path       string  `json:"path" jsonschema_description:"条目的原路径"`
	Action     string  `json:"action" jsonschema:"enum=move,enum=unit,enum=container,enum=skip,enum=undo" jsonschema_description:"move：文件移到 new_path（目录用 move 等同 unit）；unit：目录整体移动到 new_path；container：目录本身不动，子项逐个处理；skip：连同全部内容保持原位；undo：撤销这条决定"`
	NewPath    string  `json:"new_path,omitempty" jsonschema_description:"move 和 unit 必填：移动后的完整路径（文件含文件名），如 /照片/2023/IMG_0012.jpg、/项目/blog"`
	Reason     string  `json:"reason" jsonschema_description:"给用户看的一句话理由，必填"`
	Confidence float64 `json:"confidence,omitempty" jsonschema:"minimum=0,maximum=1" jsonschema_description:"把握程度 0–1，不填视为有把握；低于 0.6 交给用户确认"`
	Override   bool    `json:"override,omitempty" jsonschema_description:"unit 或 skip 的目录下已有单独决定时，true 表示确认用整体决定覆盖它们"`
}

type DecideInput struct {
	Items []DecideItem `json:"items" jsonschema:"maxItems=200" jsonschema_description:"最多 200 条"`
}

type BatchFailed struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type BatchRenamed struct {
	Path    string `json:"path"`
	NewPath string `json:"new_path"`
}

type DecideOutput struct {
	Envelope
	OK          int            `json:"ok"`
	Failed      []BatchFailed  `json:"failed,omitempty"`
	Renamed     []BatchRenamed `json:"renamed,omitempty"`
	NeedsReview int            `json:"needs_review,omitempty"`
	Overridden  []string       `json:"overridden,omitempty"`
}

type SplitUnitInput struct {
	Path string `json:"path" jsonschema_description:"整体移动或整体跳过的目录的原路径"`
}

type SplitUnitOutput struct {
	Envelope
	Released int64 `json:"released"`
}

// A target must lie under a category path unless the session has a plan.
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
		return fmt.Sprintf("new_path 不合法：%v", err)
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
		return fmt.Sprintf("目标 %s 不在任何分类路径下，而当前还没有任何分类：请先 create_category 或让用户在方案中定义目标结构", SanitizeUntrustedName(np))
	}
	paths := make([]string, len(c.cats))
	for i, cat := range c.cats {
		paths[i] = fmt.Sprintf("%s (%s)", SanitizeUntrustedName(cat.Name), db.NormalizeTargetPath(cat.Path))
	}
	return fmt.Sprintf("目标 %s 不在任何分类路径下，未写入。现有分类：%s", SanitizeUntrustedName(np), strings.Join(paths, "、"))
}

func pathUnder(p, dir string) bool {
	if dir == "/" {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// decide writes every item through db.DecideBatch. A batch-level problem is
// reported in out.Error; rejected items land in out.Failed.
func (tb *ToolBuilder) decide(ds []db.Decision) (*DecideOutput, []db.DecideOutcome, error) {
	out := &DecideOutput{}
	if msg := tb.decidingGate(); msg != "" {
		out.Error = msg
		return out, nil, nil
	}
	if len(ds) == 0 {
		out.Error = "items 不能为空"
		return out, nil, nil
	}
	if len(ds) > maxBatchItems {
		out.Error = fmt.Sprintf("一次最多 %d 条，本次 %d 条", maxBatchItems, len(ds))
		out.Hint = "分批调用，或用规则（add_rule）、脚本（run_script）处理大量同类条目"
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
		d.Reason = strings.TrimSpace(d.Reason)
		if d.Reason == "" {
			outcomes[i] = db.DecideOutcome{Err: &db.Rejection{Msg: "reason 不能为空：写一句给用户看的理由"}}
			continue
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
	if len(valid) > 0 {
		res, err := tb.repo.DecideBatch(tb.sessionID, valid)
		if err != nil {
			return nil, nil, err
		}
		for j, o := range res {
			outcomes[idx[j]] = o
		}
	}
	for i, o := range outcomes {
		if o.Err != nil {
			out.Failed = append(out.Failed, BatchFailed{Path: SanitizeUntrustedName(ds[i].Path), Error: SanitizeUntrustedName(o.Err.Error())})
			continue
		}
		out.OK++
		if o.Result.Renamed {
			out.Renamed = append(out.Renamed, BatchRenamed{Path: SanitizeUntrustedName(o.Result.Path), NewPath: SanitizeUntrustedName(o.Result.NewPath)})
		}
		if o.Result.NeedsReview {
			out.NeedsReview++
		}
		for _, p := range o.Result.Overridden {
			out.Overridden = append(out.Overridden, SanitizeUntrustedName(p))
		}
	}
	out.Success = len(out.Failed) == 0 && out.Error == ""
	if len(out.Failed) > 0 {
		out.Error = fmt.Sprintf("%d 条未写入，见 failed；其余已写入", len(out.Failed))
	}
	return out, outcomes, nil
}

func decisionFor(it DecideItem) (db.Decision, string) {
	d := db.Decision{Path: it.Path, NewPath: it.NewPath, Reason: it.Reason, Confidence: it.Confidence, Override: it.Override}
	switch it.Action {
	case actionMove:
		d.Role = db.RoleFile
	case actionUnit:
		d.Role = db.RoleUnit
	case actionContainer:
		d.Role = db.RoleContainer
	case actionSkip:
		d.Role = db.RoleSkip
	case actionUndo:
		d.Role = db.RoleUndecided
	default:
		return d, fmt.Sprintf("action 只能是 move / unit / container / skip / undo，收到 %q", SanitizeUntrustedName(it.Action))
	}
	if (it.Action == actionMove || it.Action == actionUnit) && strings.TrimSpace(it.NewPath) == "" {
		return d, "move 和 unit 需要 new_path"
	}
	return d, ""
}

func (tb *ToolBuilder) decideTool(ctx context.Context, in *DecideInput) (*DecideOutput, error) {
	if msg := tb.decidingGate(); msg != "" {
		return failed[DecideOutput](msg), nil
	}
	// Items with a malformed action are reported per item; the rest still go in.
	var ds []db.Decision
	var bad []BatchFailed
	for _, it := range in.Items {
		d, msg := decisionFor(it)
		if msg != "" {
			bad = append(bad, BatchFailed{Path: SanitizeUntrustedName(normalizePath(it.Path)), Error: msg})
			continue
		}
		ds = append(ds, d)
	}
	if len(ds) == 0 && len(bad) == 0 {
		return failed[DecideOutput]("items 不能为空"), nil
	}
	if len(in.Items) > maxBatchItems {
		return failed[DecideOutput](fmt.Sprintf("一次最多 %d 条，本次 %d 条", maxBatchItems, len(in.Items)), "分批调用，或用规则（add_rule）、脚本（run_script）处理大量同类条目"), nil
	}
	var out *DecideOutput
	if len(ds) > 0 {
		var err error
		out, _, err = tb.decide(ds)
		if err != nil {
			return nil, err
		}
	} else {
		out = &DecideOutput{}
	}
	if len(bad) > 0 {
		out.Failed = append(bad, out.Failed...)
		out.Success = false
		out.Error = fmt.Sprintf("%d 条未写入，见 failed；其余已写入", len(out.Failed))
	}
	return out, nil
}

func (tb *ToolBuilder) splitUnit(ctx context.Context, in *SplitUnitInput) (*SplitUnitOutput, error) {
	if msg := tb.decidingGate(); msg != "" {
		return failed[SplitUnitOutput](msg), nil
	}
	n, err := tb.repo.SplitUnit(tb.sessionID, in.Path)
	if err != nil {
		return nil, err
	}
	out := succeeded[SplitUnitOutput]()
	out.Released = n
	return out, nil
}

func (tb *ToolBuilder) decideOne(d db.Decision) (*db.DecideResult, error) {
	out, outcomes, err := tb.decide([]db.Decision{d})
	if err != nil {
		return nil, err
	}
	if out.Error != "" && len(outcomes) == 0 {
		return nil, &db.Rejection{Msg: out.Error}
	}
	if outcomes[0].Err != nil {
		return nil, outcomes[0].Err
	}
	return outcomes[0].Result, nil
}

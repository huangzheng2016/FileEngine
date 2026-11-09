package db

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

var ErrRuleNotFound = errors.New("规则不存在")

var ruleVarRe = regexp.MustCompile(`\{([a-z_]+)\}`)

var ruleVars = map[string]bool{
	"name": true, "stem": true, "ext": true, "year": true, "month": true, "day": true,
	"parent": true, "rel": true, "taken_year": true, "taken_month": true, "camera": true,
}

func NormalizeRule(rule *PlanRule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	rule.Target = strings.TrimSpace(rule.Target)
	rule.PathPrefix = strings.Trim(strings.TrimSpace(rule.PathPrefix), "/")
	exts := make([]string, 0)
	for e := range ParseExts(rule.Exts) {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	rule.Exts = strings.Join(exts, ",")
	if rule.Name == "" {
		return reject("规则需要名称 name")
	}
	if rule.Target == "" {
		return reject("规则需要目标模板 target")
	}
	for _, m := range ruleVarRe.FindAllStringSubmatch(rule.Target, -1) {
		if !ruleVars[m[1]] {
			return reject("目标模板里有未知变量 {%s}（可用 name stem ext year month day parent rel taken_year taken_month camera）", m[1])
		}
	}
	if err := ValidateTargetPath(ruleVarRe.ReplaceAllString(rule.Target, "x")); err != nil {
		return reject("目标模板不合法：%v", err)
	}
	if rule.NameRegex != "" {
		if _, err := regexp.Compile(rule.NameRegex); err != nil {
			return reject("name_regex 无法解析：%v", err)
		}
	}
	switch rule.Role {
	case "", RoleFile:
		rule.Role = RoleFile
	case RoleUnit:
		rule.FileType = "directory"
	default:
		return reject("规则角色只能是 file 或 unit")
	}
	switch rule.FileType {
	case "", "file", "directory":
	default:
		return reject("file_type 只能是 file 或 directory")
	}
	if rule.MinSize < 0 || rule.MaxSize < 0 || (rule.MaxSize > 0 && rule.MaxSize < rule.MinSize) {
		return reject("min_size / max_size 不合法")
	}
	return nil
}

func (r *Repository) ListRules(sessionID uint) ([]PlanRule, error) {
	var rules []PlanRule
	err := r.db.Where("session_id = ?", sessionID).Order("position, id").Find(&rules).Error
	return rules, err
}

func (r *Repository) GetRule(id uint) (*PlanRule, error) {
	var rule PlanRule
	if err := r.db.Take(&rule, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRuleNotFound
		}
		return nil, err
	}
	return &rule, nil
}

func (r *Repository) CreateRule(rule *PlanRule) error {
	if err := NormalizeRule(rule); err != nil {
		return err
	}
	rule.ID, rule.Matched = 0, 0
	return WithRetry(func() error {
		return r.db.Transaction(func(tx *gorm.DB) error {
			if rule.Position <= 0 {
				var maxPos *int
				if err := tx.Model(&PlanRule{}).Where("session_id = ?", rule.SessionID).Select("MAX(position)").Scan(&maxPos).Error; err != nil {
					return err
				}
				rule.Position = 1
				if maxPos != nil {
					rule.Position = *maxPos + 1
				}
			}
			return tx.Create(rule).Error
		})
	})
}

func (r *Repository) UpdateRule(id uint, apply func(*PlanRule)) (*PlanRule, error) {
	rule, err := r.GetRule(id)
	if err != nil {
		return nil, err
	}
	apply(rule)
	rule.ID = id
	if err := NormalizeRule(rule); err != nil {
		return nil, err
	}
	rule.UpdatedAt = time.Now()
	if err := r.db.Model(&PlanRule{}).Where("id = ?", id).Updates(map[string]any{
		"position": rule.Position, "name": rule.Name, "exts": rule.Exts, "name_regex": rule.NameRegex,
		"path_prefix": rule.PathPrefix, "file_type": rule.FileType, "min_size": rule.MinSize, "max_size": rule.MaxSize,
		"target": rule.Target, "role": rule.Role, "updated_at": rule.UpdatedAt,
	}).Error; err != nil {
		return nil, err
	}
	return rule, nil
}

func (r *Repository) DeleteRule(id uint) error {
	res := r.db.Delete(&PlanRule{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrRuleNotFound
	}
	return nil
}

type ruleMatcher struct {
	rule  *PlanRule
	exts  map[string]bool
	re    *regexp.Regexp
	named bool // target references the entry's own name
}

func compileRule(rule *PlanRule) (*ruleMatcher, error) {
	if err := NormalizeRule(rule); err != nil {
		return nil, err
	}
	m := &ruleMatcher{rule: rule, exts: ParseExts(rule.Exts)}
	if rule.NameRegex != "" {
		m.re = regexp.MustCompile(rule.NameRegex)
	}
	m.named = strings.Contains(rule.Target, "{name}") || strings.Contains(rule.Target, "{stem}") || strings.Contains(rule.Target, "{rel}")
	return m, nil
}

func (m *ruleMatcher) match(e *FileEntry) bool {
	r := m.rule
	if r.FileType != "" && e.FileType != r.FileType {
		return false
	}
	if r.FileType == "" && e.FileType == "symlink" {
		return false
	}
	if len(m.exts) > 0 && !m.exts[FileExt(e.Name)] {
		return false
	}
	if m.re != nil && !m.re.MatchString(e.Name) {
		return false
	}
	if r.PathPrefix != "" && !strings.HasPrefix(e.OriginalPath, r.PathPrefix+"/") {
		return false
	}
	if r.MinSize > 0 && e.Size < r.MinSize {
		return false
	}
	if r.MaxSize > 0 && e.Size > r.MaxSize {
		return false
	}
	return true
}

func pathSafe(s, fallback string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "/", "_"))
	if s == "" || s == "." || s == ".." {
		return fallback
	}
	return s
}

// A template that never names the entry ({name} {stem} {rel}) is a
// directory: the entry's name is appended.
func RenderRuleTarget(rule *PlanRule, e *FileEntry) (string, error) {
	when := e.ModTime
	if e.TakenAt != nil {
		when = *e.TakenAt
	}
	ext := FileExt(e.Name)
	stem := e.Name
	if ext != "" {
		stem = e.Name[:len(e.Name)-len(ext)-1]
	}
	parent := ""
	if e.ParentPath != "" && e.ParentPath != "." {
		parent = path.Base(e.ParentPath)
	}
	rel := e.OriginalPath
	if rule.PathPrefix != "" {
		rel = strings.TrimPrefix(rel, rule.PathPrefix+"/")
	}
	takenYear, takenMonth := "未知日期", "未知日期"
	if e.TakenAt != nil {
		takenYear, takenMonth = e.TakenAt.Format("2006"), e.TakenAt.Format("01")
	}
	vals := map[string]string{
		"name": pathSafe(e.Name, "未命名"), "stem": pathSafe(stem, "未命名"), "ext": pathSafe(ext, "无扩展名"),
		"year": when.Format("2006"), "month": when.Format("01"), "day": when.Format("02"),
		"parent": pathSafe(parent, "根目录"), "rel": rel,
		"taken_year": takenYear, "taken_month": takenMonth, "camera": pathSafe(e.Camera, "未知相机"),
	}
	out := ruleVarRe.ReplaceAllStringFunc(rule.Target, func(v string) string { return vals[v[1:len(v)-1]] })
	named := strings.Contains(rule.Target, "{name}") || strings.Contains(rule.Target, "{stem}") || strings.Contains(rule.Target, "{rel}")
	if !named {
		out = strings.TrimSuffix(out, "/") + "/" + vals["name"]
	}
	if err := ValidateTargetPath(out); err != nil {
		return "", err
	}
	return NormalizeTargetPath(out), nil
}

type RuleSample struct {
	Path   string `json:"original_path"`
	Target string `json:"new_path"`
}

type RuleApplyStat struct {
	RuleID  uint         `json:"rule_id"`
	Name    string       `json:"name"`
	Matched int          `json:"matched"`
	Skipped int          `json:"skipped"` // matched but not decided: bad target, or rejected by the decision rules
	Samples []RuleSample `json:"samples,omitempty"`
}

func (r *Repository) ruleCandidates(sessionID uint, rule *PlanRule, fn func([]FileEntry) error) error {
	var after uint
	for {
		q := r.db.Model(&FileEntry{}).Where("scan_session_id = ? AND role = ? AND id > ?", sessionID, RoleUndecided, after)
		if rule.FileType != "" {
			q = q.Where("file_type = ?", rule.FileType)
		} else {
			q = q.Where("file_type <> 'symlink'")
		}
		if rule.PathPrefix != "" {
			cond, args := subtreeCond(q, "original_path", rule.PathPrefix)
			q = q.Where(cond, args...)
		}
		if rule.MinSize > 0 {
			q = q.Where("size >= ?", rule.MinSize)
		}
		if rule.MaxSize > 0 {
			q = q.Where("size <= ?", rule.MaxSize)
		}
		if rule.Exts != "" {
			var conds []string
			var args []any
			for _, e := range strings.Split(rule.Exts, ",") {
				conds = append(conds, ciLikeCondition(q, "name"))
				args = append(args, "%."+likeLiteral(e))
			}
			q = q.Where("("+strings.Join(conds, " OR ")+")", args...)
		}
		var rows []FileEntry
		if err := q.Order("id").Limit(5000).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if err := fn(rows); err != nil {
			return err
		}
		after = rows[len(rows)-1].ID
	}
}

// First match by position wins. With preview nothing is written. Writes go
// through DecideBatch, so validation, the invariant and collision naming
// apply.
func (r *Repository) ApplyRules(sessionID uint, ruleID uint, preview bool) ([]RuleApplyStat, error) {
	rules, err := r.ListRules(sessionID)
	if err != nil {
		return nil, err
	}
	claimed := map[uint]bool{}
	stats := []RuleApplyStat{}
	for i := range rules {
		rule := &rules[i]
		if ruleID != 0 && rule.ID != ruleID {
			continue
		}
		m, err := compileRule(rule)
		if err != nil {
			return nil, err
		}
		stat := RuleApplyStat{RuleID: rule.ID, Name: rule.Name}
		var decisions []Decision
		err = r.ruleCandidates(sessionID, rule, func(rows []FileEntry) error {
			for j := range rows {
				e := &rows[j]
				if claimed[e.ID] || !m.match(e) {
					continue
				}
				target, err := RenderRuleTarget(rule, e)
				if err != nil {
					stat.Skipped++
					continue
				}
				if preview {
					claimed[e.ID] = true
					stat.Matched++
					if len(stat.Samples) < 10 {
						stat.Samples = append(stat.Samples, RuleSample{Path: e.OriginalPath, Target: target})
					}
					continue
				}
				decisions = append(decisions, Decision{Path: e.OriginalPath, Role: rule.Role, NewPath: target, Reason: "规则：" + rule.Name})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if !preview {
			// Shallow first, so a unit rule claims a directory before its descendants.
			sort.SliceStable(decisions, func(a, b int) bool {
				return strings.Count(decisions[a].Path, "/") < strings.Count(decisions[b].Path, "/")
			})
			outcomes, err := r.DecideBatch(sessionID, decisions)
			if err != nil {
				return nil, err
			}
			for j, o := range outcomes {
				if o.Err != nil {
					stat.Skipped++
				}
				if o.Err == nil && o.Result != nil {
					stat.Matched++
					if len(stat.Samples) < 10 {
						stat.Samples = append(stat.Samples, RuleSample{Path: decisions[j].Path, Target: o.Result.NewPath})
					}
				}
			}
			if err := r.db.Model(&PlanRule{}).Where("id = ?", rule.ID).Update("matched", stat.Matched).Error; err != nil {
				return nil, err
			}
		}
		stats = append(stats, stat)
	}
	return stats, nil
}

func (r *Repository) PrepareSession(sessionID uint) error {
	session, err := r.GetSession(sessionID)
	if err != nil {
		return err
	}
	if _, err := r.ApplyScope(session); err != nil {
		return fmt.Errorf("apply scope: %w", err)
	}
	if _, err := r.MarkSensitive(sessionID); err != nil {
		return fmt.Errorf("mark sensitive: %w", err)
	}
	if _, err := r.ApplyRules(sessionID, 0, false); err != nil {
		return fmt.Errorf("apply rules: %w", err)
	}
	return nil
}

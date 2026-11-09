package agent

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"path"
	"sort"
	"strings"

	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

const (
	reviewSamplesPerCategory = 12
	reviewUncategorizedMax   = 20
	reviewSplitDirsMax       = 15
	reviewSplitMinFiles      = 3
	reviewSplitMinCategories = 3
)

type reviewSample struct {
	from, to, reason string
}

type reviewCategory struct {
	cat     db.Category
	norm    string
	count   int
	samples []reviewSample
	rng     *rand.Rand
}

// The fixed seed makes the material reproducible for the same data.
func (c *reviewCategory) add(s reviewSample) {
	c.count++
	if len(c.samples) < reviewSamplesPerCategory {
		c.samples = append(c.samples, s)
		return
	}
	if j := c.rng.IntN(c.count); j < reviewSamplesPerCategory {
		c.samples[j] = s
	}
}

type splitDir struct {
	dir   string
	files int
	cats  map[string]int
}

func BuildReviewMaterial(repo *db.Repository, session *db.ScanSession, tagging *db.AgentRun) (string, error) {
	cats, err := repo.ListCategories(session.FilesystemID)
	if err != nil {
		return "", err
	}
	rcs := make([]*reviewCategory, len(cats))
	for i, c := range cats {
		rcs[i] = &reviewCategory{cat: c, norm: normalizeCategoryPath(c.Path), rng: rand.New(rand.NewPCG(1, uint64(c.ID)))}
	}
	var uncategorized []reviewSample
	uncategorizedCount := 0
	dirs := map[string]*splitDir{}
	err = repo.EachEntry(session.ID, true, func(e db.EntryBrief) bool {
		target := normalizeCategoryPath(e.NewPath)
		var best *reviewCategory
		for _, rc := range rcs {
			if rc.norm != "" && (target == rc.norm || strings.HasPrefix(target, rc.norm+"/")) && (best == nil || len(rc.norm) > len(best.norm)) {
				best = rc
			}
		}
		s := reviewSample{from: e.OriginalPath, to: e.NewPath, reason: e.Reason}
		catName := "（无分类）"
		if best == nil {
			uncategorizedCount++
			if len(uncategorized) < reviewUncategorizedMax {
				uncategorized = append(uncategorized, s)
			}
		} else {
			best.add(s)
			catName = best.cat.Name
		}
		if e.FileType != "directory" {
			parent := path.Dir(e.OriginalPath)
			d := dirs[parent]
			if d == nil {
				d = &splitDir{dir: parent, cats: map[string]int{}}
				dirs[parent] = d
			}
			d.files++
			d.cats[catName]++
		}
		return true
	})
	if err != nil {
		return "", fmt.Errorf("review material: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("# 复核材料（系统统计）\n\n")
	sb.WriteString("## 各分类抽样\n\n")
	anyPlanned := false
	for _, rc := range rcs {
		if rc.count == 0 {
			continue
		}
		anyPlanned = true
		sb.WriteString(fmt.Sprintf("### %s（%s，共 %d 项）\n\n", rc.cat.Name, rc.cat.Path, rc.count))
		writeSamples(&sb, rc.samples)
	}
	if !anyPlanned {
		sb.WriteString("（没有任何规划落在分类下）\n\n")
	}
	var empty []string
	for _, rc := range rcs {
		if rc.count == 0 {
			empty = append(empty, fmt.Sprintf("%s（%s）", rc.cat.Name, rc.cat.Path))
		}
	}
	if len(empty) > 0 {
		sb.WriteString("## 没有规划的分类\n\n" + strings.Join(empty, "、") + "\n\n")
	}
	if uncategorizedCount > 0 {
		sb.WriteString(fmt.Sprintf("## 不在任何分类下的规划（共 %d 项）\n\n", uncategorizedCount))
		writeSamples(&sb, uncategorized)
	}

	var split []*splitDir
	for _, d := range dirs {
		if d.files >= reviewSplitMinFiles && len(d.cats) >= reviewSplitMinCategories {
			split = append(split, d)
		}
	}
	sort.Slice(split, func(i, j int) bool {
		if len(split[i].cats) != len(split[j].cats) {
			return len(split[i].cats) > len(split[j].cats)
		}
		return split[i].dir < split[j].dir
	})
	if len(split) > 0 {
		sb.WriteString("## 同目录文件被分到多个分类\n\n")
		for i, d := range split {
			if i == reviewSplitDirsMax {
				sb.WriteString(fmt.Sprintf("- …另有 %d 个目录\n", len(split)-reviewSplitDirsMax))
				break
			}
			sb.WriteString(fmt.Sprintf("- %s（%d 个文件）：%s\n", SanitizeUntrustedName(d.dir), d.files, topExts(d.cats, 6)))
		}
		sb.WriteString("\n")
	}

	var overlaps []string
	for i := range rcs {
		for j := i + 1; j < len(rcs); j++ {
			a, b := rcs[i], rcs[j]
			if categoryPathsOverlap(a.norm, b.norm) || normalizeCategoryName(a.cat.Name) == normalizeCategoryName(b.cat.Name) {
				overlaps = append(overlaps, fmt.Sprintf("- %s（%s）与 %s（%s）", a.cat.Name, a.cat.Path, b.cat.Name, b.cat.Path))
			}
		}
	}
	if len(overlaps) > 0 {
		sb.WriteString("## 路径重叠的分类\n\n" + strings.Join(overlaps, "\n") + "\n\n")
	}

	sb.WriteString(reviewFailureSection(repo, session, tagging))
	return sb.String(), nil
}

func writeSamples(sb *strings.Builder, samples []reviewSample) {
	for _, s := range samples {
		sb.WriteString(fmt.Sprintf("- %s → %s", SanitizeUntrustedName(s.from), SanitizeUntrustedName(s.to)))
		if r := strings.TrimSpace(s.reason); r != "" {
			sb.WriteString("（" + SanitizeUntrustedName(oneLine(r)) + "）")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
}

func reviewFailureSection(repo *db.Repository, session *db.ScanSession, tagging *db.AgentRun) string {
	var sb strings.Builder
	if tagging != nil {
		if tagging.Error != "" {
			sb.WriteString("运行错误：" + tagging.Error + "\n\n")
		}
		sb.WriteString(failureSections(tagging.Summary))
	}
	if b, err := repo.CountUntaggedByType(session.ID); err == nil && b.Files > 0 {
		sb.WriteString(fmt.Sprintf("仍有 %d 个文件未处理。\n\n", b.Files))
	}
	if sb.Len() == 0 {
		return ""
	}
	return "## 失败与遗漏\n\n" + sb.String()
}

func failureSections(report string) string {
	var sb strings.Builder
	keep := false
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, "## ") {
			keep = strings.Contains(line, "失败") || strings.Contains(line, "对账") || strings.Contains(line, "遗漏")
			if keep {
				line = "#" + line
			}
		}
		if keep {
			sb.WriteString(line + "\n")
		}
	}
	return sb.String()
}

// Reflection problems never fail the review.
func (a *Agent) RunReview(ctx context.Context, logger *Logger, tagging *db.AgentRun) (string, error) {
	material, err := BuildReviewMaterial(a.repo, a.session, tagging)
	if err != nil {
		return "", err
	}
	report, err := a.runMainAgent(ctx, logger, mainRunSpec{
		label:       "review",
		maxStep:     config.Get().Agent.MaxStep,
		instruction: reviewSystemPrompt(config.Get().Agent, a.session),
		messages:    []*schema.Message{{Role: schema.User, Content: material + "请复核以上规划，修正明显的错误，最后输出复核报告。"}},
	})
	if err != nil {
		return "", err
	}
	if report == "" {
		report = "复查完成，助手没有给出报告。"
	}
	if res, err := a.Reflect(ctx, logger, tagging); err != nil {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		log.Printf("review: reflection for session %d failed: %v", a.sessionID, err)
		logger.LogMessage("system", "记忆整理失败："+err.Error())
	} else if n := res.Added + res.Updated + res.Deleted; n > 0 {
		report += fmt.Sprintf("\n\n---\n\n记忆已更新：新增 %d 条，修改 %d 条，删除 %d 条。", res.Added, res.Updated, res.Deleted)
	}
	return report, nil
}

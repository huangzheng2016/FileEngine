package agent

import (
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"FileEngine/internal/db"
)

// categoryGuard tracks the category-set snapshot the agent last saw, so
// create_category can refuse to run against a stale (or never-seen) list.
// One guard lives in each ToolBuilder, i.e. per agent worker / per instruct
// run — the lifetime of one session-scoped conversation. It is in-memory
// only; nothing is persisted and no DB schema changes.
type categoryGuard struct {
	mu sync.Mutex
	fp string
	// set distinguishes "never saw any list" from a recorded empty set:
	// both must reject the first create, but only the former does.
	set bool
}

// categoryFingerprint renders the version of a category set as the count
// plus the exact set of names. Renames, creations and deletions all change
// it; a path-only edit does not (per the agreed design: 数量+名称集合).
func categoryFingerprint(names []string) string {
	sort.Strings(names)
	return strconv.Itoa(len(names)) + "\x1f" + strings.Join(names, "\x1f")
}

// note records the snapshot the agent is about to see.
func (g *categoryGuard) note(names []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fp = categoryFingerprint(names)
	g.set = true
}

// last returns the recorded snapshot and whether any was ever recorded.
func (g *categoryGuard) last() (fp string, set bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fp, g.set
}

func normalizeCategoryName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// normalizeCategoryPath folds case and strips separators so that prefix
// comparison is segment-based and case-insensitive.
func normalizeCategoryPath(p string) string {
	return strings.Trim(path.Clean(strings.ToLower(strings.TrimSpace(p))), "/")
}

// categoryPathsOverlap reports whether two normalized paths are equal or one
// sits under the other (segment-aware: "photos" vs "photos2" does not bleed).
func categoryPathsOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// similarCategory pairs an existing category with why it matched.
type similarCategory struct {
	usage  db.CategoryUsage
	reason string
}

// findSimilarCategories splits existing categories related to (name, path)
// into two hit classes:
//   - hard: objective duplicates — a normalized identical name (trim +
//     case-fold) or a normalized identical path. Creation must be refused.
//   - soft: fuzzy lookalikes — a segment-aware path-prefix overlap in either
//     direction (containment) that is not already a hard hit. Creation
//     proceeds; these ride along as reference info for the model to judge.
func findSimilarCategories(usages []db.CategoryUsage, name, path string) (hard, soft []similarCategory) {
	nName := normalizeCategoryName(name)
	nPath := normalizeCategoryPath(path)
	for _, u := range usages {
		uName := normalizeCategoryName(u.Name)
		uPath := normalizeCategoryPath(u.Path)
		if nName != "" && uName == nName {
			hard = append(hard, similarCategory{usage: u, reason: "名称相同（忽略大小写）"})
			continue
		}
		if nPath != "" && uPath == nPath {
			hard = append(hard, similarCategory{usage: u, reason: "路径相同"})
			continue
		}
		if categoryPathsOverlap(nPath, uPath) {
			soft = append(soft, similarCategory{usage: u, reason: "路径重叠"})
		}
	}
	return hard, soft
}

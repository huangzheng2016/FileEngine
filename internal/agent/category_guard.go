package agent

import (
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"FileEngine/internal/db"
)

// categoryGuard holds the category snapshot the agent last saw, so
// create_category can refuse to run against a stale or never-seen list.
type categoryGuard struct {
	mu sync.Mutex
	fp string
	// set distinguishes "never saw any list" from a recorded empty set:
	// both must reject the first create, but only the former does.
	set bool
}

// Path-only edits deliberately keep the fingerprint unchanged.
func categoryFingerprint(names []string) string {
	sort.Strings(names)
	return strconv.Itoa(len(names)) + "\x1f" + strings.Join(names, "\x1f")
}

func (g *categoryGuard) note(names []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fp = categoryFingerprint(names)
	g.set = true
}

func (g *categoryGuard) last() (fp string, set bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fp, g.set
}

func normalizeCategoryName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeCategoryPath(p string) string {
	return strings.Trim(path.Clean(strings.ToLower(strings.TrimSpace(p))), "/")
}

// Segment-aware: "photos" and "photos2" do not overlap.
func categoryPathsOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

type similarCategory struct {
	usage  db.CategoryUsage
	reason string
}

// Hard hits (normalized identical name or path) block creation; soft hits
// (path containment either way) are only reported for the model to judge.
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

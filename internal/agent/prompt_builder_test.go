package agent

import (
	"fmt"
	"strings"
	"testing"

	"FileEngine/internal/db"
)

// The injected category summary must list the complete category system —
// used and unused categories alike, with no count cap — so the agent never
// plans against a partial view.
func TestBuildCategorySummaryListsAllCategories(t *testing.T) {
	var usages []db.CategoryUsage
	usages = append(usages,
		db.CategoryUsage{Name: "照片", Path: "/photos", FileCount: 5},
		db.CategoryUsage{Name: "文档", Path: "/docs", FileCount: 12},
	)
	// Well beyond the old 20-entry cap, all unused.
	for i := 0; i < 25; i++ {
		usages = append(usages, db.CategoryUsage{
			Name: fmt.Sprintf("空分类%02d", i), Path: fmt.Sprintf("/empty/%02d", i),
		})
	}

	out := buildCategorySummary(usages)

	for _, u := range usages {
		if !strings.Contains(out, u.Name+" ("+u.Path+")") {
			t.Fatalf("summary missing category %s (%s)", u.Name, u.Path)
		}
	}

	// Usage counts shown for used categories, omitted for unused ones.
	if !strings.Contains(out, "文档 (/docs): 已归入 12 项") {
		t.Fatal("used category must show its count")
	}
	if strings.Contains(out, "空分类00 (/empty/00): 已归入 0 项") {
		t.Fatal("unused category must not show a zero count line")
	}

	// Sorted by usage descending: 文档 (12) precedes 照片 (5), used precede unused.
	di := strings.Index(out, "文档 (/docs)")
	pi := strings.Index(out, "照片 (/photos)")
	ei := strings.Index(out, "空分类00 (/empty/00)")
	if di == -1 || pi == -1 || ei == -1 || !(di < pi && pi < ei) {
		t.Fatalf("usage-descending order broken: docs@%d photos@%d empty@%d", di, pi, ei)
	}
}

func TestBuildCategorySummaryEmpty(t *testing.T) {
	if got := buildCategorySummary(nil); got != "" {
		t.Fatalf("empty usages: got %q, want empty", got)
	}
}

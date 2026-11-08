package agent

import (
	"fmt"
	"strings"
	"testing"

	"FileEngine/internal/db"
)

func TestBuildCategorySummaryNameOrderNoCounts(t *testing.T) {
	var usages []db.CategoryUsage
	usages = append(usages,
		db.CategoryUsage{Name: "照片", Path: "/photos", FileCount: 5, Description: "相机照片"},
		db.CategoryUsage{Name: "文档", Path: "/docs", FileCount: 12},
	)
	for i := 0; i < 25; i++ {
		usages = append(usages, db.CategoryUsage{Name: fmt.Sprintf("空分类%02d", i), Path: fmt.Sprintf("/empty/%02d", i)})
	}
	out := buildCategorySummary(usages)
	for _, u := range usages {
		if !strings.Contains(out, u.Name+"："+u.Path) {
			t.Fatalf("summary missing %s", u.Name)
		}
	}
	if strings.Contains(out, "已归入") {
		t.Fatalf("no usage counts: %s", out)
	}
	if strings.Index(out, "文档") > strings.Index(out, "照片") || !strings.Contains(out, "放什么：相机照片") {
		t.Fatalf("name order + description: %s", out)
	}
	if buildCategorySummary(nil) != "" {
		t.Fatal("empty")
	}
}

func seedSharedContext(t *testing.T) (*db.Repository, *db.ScanSession) {
	t.Helper()
	repo := setupToolTestRepo(t)
	session := &db.ScanSession{FilesystemID: 7, Goal: "照片按年份整理，项目保持完整"}
	if err := repo.CreateSession(session); err != nil {
		t.Fatal(err)
	}
	for _, c := range []db.Category{
		{FilesystemID: 7, Name: "照片", Path: "/照片", Description: "所有照片\n和视频", Structure: "{年}/{原文件夹名}"},
		{FilesystemID: 7, Name: "代码", Path: "/代码"},
	} {
		c := c
		if err := repo.CreateCategory(&c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.SavePlan(session.ID, "# 方案\n照片按年", 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateRule(&db.PlanRule{SessionID: session.ID, Name: "照片按年", Exts: "jpg", Target: "照片/{year}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertMemory(&db.MemoryItem{Scope: db.MemoryScopeFilesystem, ScopeID: 7, Kind: db.MemoryCorrection, Key: "c", Content: "截图不算照片", Path: "Photos"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []db.FileEntry{
		{ScanSessionID: session.ID, OriginalPath: "Photos", Name: "Photos", FileType: "directory", Description: "手机相册导出"},
		{ScanSessionID: session.ID, OriginalPath: "Photos/2023", Name: "2023", FileType: "directory", ParentPath: "Photos"},
		{ScanSessionID: session.ID, OriginalPath: "Photos/2023/trip", Name: "trip", FileType: "directory", ParentPath: "Photos/2023", Description: "2023 旅行"},
	} {
		e := e
		if err := repo.CreateFile(&e); err != nil {
			t.Fatal(err)
		}
	}
	return repo, session
}

func TestSharedContextSectionsAndStability(t *testing.T) {
	repo, session := seedSharedContext(t)
	out, err := BuildSharedContext(repo, session)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{"## 用户目标\n\n照片按年份整理", "## 整理方案（草稿，用户尚未确认）", "## 分类", "- 代码：/代码", "- 照片：/照片；放什么：所有照片 和视频；结构：{年}/{原文件夹名}", "## 规则", "照片按年：扩展名 jpg → 照片/{year}", "## 记忆", "截图不算照片"}
	last := -1
	for _, s := range order {
		i := strings.Index(out, s)
		if i <= last {
			t.Fatalf("%q missing or out of order:\n%s", s, out)
		}
		last = i
	}
	// Planning activity (usage counts, rule matches, recall hits) must not
	// change the block.
	if _, _, err := repo.SetTargetUnique(session.ID, "Photos", "/照片/Photos", "directory"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ApplyRules(session.ID, 0, false); err != nil {
		t.Fatal(err)
	}
	items, _ := repo.ListLongTermMemory(7)
	_ = repo.BumpMemoryHits([]uint{items[0].ID})
	again, _ := BuildSharedContext(repo, session)
	if again != out {
		t.Fatalf("shared context changed without a content edit:\n%s\n---\n%s", out, again)
	}
	msg := SharedContextMessage(out)
	if !strings.HasPrefix(msg.Content, "# 会话共享上下文\n\n") {
		t.Fatalf("message heading: %q", msg.Content[:30])
	}
}

func TestUnitContext(t *testing.T) {
	repo, session := seedSharedContext(t)
	uc, err := LoadUnitContext(repo, session, []string{"Photos/2023/trip"}, "## 其他 worker 的发现\n\n- 某发现\n")
	if err != nil {
		t.Fatal(err)
	}
	out := RenderUnitContext(uc)
	for _, want := range []string{"## 上级目录", "- Photos：手机相册导出", "- Photos/2023/trip：2023 旅行", "## 与此位置相关的记忆", "截图不算照片", "## 其他 worker 的发现"} {
		if !strings.Contains(out, want) {
			t.Fatalf("unit context missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Photos/2023：") {
		t.Fatal("ancestors without description are skipped")
	}
}

package agent

import (
	"strings"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

// Run-end prune: AI-created categories with zero planned files are deleted
// and recorded; user-created and non-empty categories are never touched.
func TestPruneEmptyAgentCategories(t *testing.T) {
	repo := setupToolTestRepo(t)
	fs := &db.Filesystem{Name: "fs", Protocol: "local", BasePath: "/data"}
	if err := repo.CreateFilesystem(fs); err != nil {
		t.Fatalf("create fs: %v", err)
	}
	session := &db.ScanSession{FilesystemID: fs.ID, Status: "tagged"}
	if err := repo.CreateSession(session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	mk := func(name, path string, agentCreated bool) {
		t.Helper()
		if err := repo.CreateCategory(&db.Category{FilesystemID: fs.ID, Name: name, Path: path, AgentCreated: agentCreated, AgentEditable: agentCreated}); err != nil {
			t.Fatalf("seed category %s: %v", name, err)
		}
	}
	mk("监控录像", "/surveillance", true)  // AI, empty → pruned
	mk("网站备份", "/website", true)       // AI, has planned file → kept
	mk("用户空分类", "/user-empty", false)  // user, empty → kept
	mk("未分类", "/uncategorized", false) // fallback, empty → kept

	seedFile(t, repo, session.ID, "in/a.tar.gz", "file")
	if _, err := repo.SetTargetAtomic(session.ID, "in/a.tar.gz", "/website/a.tar.gz", "planned", "file"); err != nil {
		t.Fatalf("set target: %v", err)
	}

	o := NewOrchestratorAgent(repo, config.RemoteFSConfig{}, &db.ModelProvider{}, session, session.ID, NewLogger(nil, session.ID))
	pruned := o.pruneEmptyAgentCategories()

	if len(pruned) != 1 || !strings.Contains(pruned[0], "监控录像") {
		t.Fatalf("pruned = %v, want only 监控录像", pruned)
	}
	for _, name := range []string{"网站备份", "用户空分类", "未分类"} {
		if _, err := repo.GetCategoryByName(fs.ID, name); err != nil {
			t.Fatalf("%s must survive: %v", name, err)
		}
	}
	if _, err := repo.GetCategoryByName(fs.ID, "监控录像"); err == nil {
		t.Fatal("empty AI category must be deleted")
	}

	section := emptyCategoryPruneSection(pruned)
	if !strings.Contains(section, "空分类清理（1）") || !strings.Contains(section, "监控录像") {
		t.Fatalf("section missing prune record:\n%s", section)
	}
	if emptyCategoryPruneSection(nil) != "" {
		t.Fatal("no prune → no section")
	}

}

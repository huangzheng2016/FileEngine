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

// Cross-session safety (review P2-1): a category created and filled by an
// EARLIER session has zero plans in the current session — the prune must
// judge emptiness filesystem-wide and never delete it (rescan workflows).
func TestPruneEmptyAgentCategories_CrossSession(t *testing.T) {
	repo := setupToolTestRepo(t)
	fs := &db.Filesystem{Name: "fs", Protocol: "local", BasePath: "/data"}
	if err := repo.CreateFilesystem(fs); err != nil {
		t.Fatalf("create fs: %v", err)
	}
	sessA := &db.ScanSession{FilesystemID: fs.ID, Status: "tagged"}
	if err := repo.CreateSession(sessA); err != nil {
		t.Fatalf("create session A: %v", err)
	}
	sessB := &db.ScanSession{FilesystemID: fs.ID, Status: "tagged"}
	if err := repo.CreateSession(sessB); err != nil {
		t.Fatalf("create session B: %v", err)
	}

	mk := func(name, path string, agentCreated bool) {
		t.Helper()
		if err := repo.CreateCategory(&db.Category{FilesystemID: fs.ID, Name: name, Path: path, AgentCreated: agentCreated, AgentEditable: agentCreated}); err != nil {
			t.Fatalf("seed category %s: %v", name, err)
		}
	}
	mk("历史分类", "/history-used", true) // filled by session A only
	mk("真空分类", "/truly-empty", true)  // empty across all sessions

	// Planned file under 历史分类, but ONLY in session A.
	seedFile(t, repo, sessA.ID, "in/site.tar.gz", "file")
	if _, err := repo.SetTargetAtomic(sessA.ID, "in/site.tar.gz", "/history-used/site.tar.gz", "planned", "file"); err != nil {
		t.Fatalf("set target: %v", err)
	}

	// The run-ending prune for session B must keep 历史分类 (zero in B, used in A).
	o := NewOrchestratorAgent(repo, config.RemoteFSConfig{}, &db.ModelProvider{}, sessB, sessB.ID, NewLogger(nil, sessB.ID))
	pruned := o.pruneEmptyAgentCategories()

	if _, err := repo.GetCategoryByName(fs.ID, "历史分类"); err != nil {
		t.Fatalf("category used by another session must survive the prune: %v", err)
	}
	if _, err := repo.GetCategoryByName(fs.ID, "真空分类"); err == nil {
		t.Fatal("category empty across ALL sessions must be pruned")
	}
	if len(pruned) != 1 || !strings.Contains(pruned[0], "真空分类") {
		t.Fatalf("pruned = %v, want only 真空分类", pruned)
	}
}

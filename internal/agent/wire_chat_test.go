package agent

import (
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// wireChatAgent is an Agent whose model is the fake server (the real model
// factory), with one scanned file for the selection.
func wireChatAgent(t *testing.T, ws *wireServer) (*db.Repository, *Agent, *db.ScanSession) {
	t.Helper()
	repo := setupToolTestRepo(t)
	old := config.Get()
	config.Update(&config.Config{Agent: config.AgentConfig{MaxStep: 20, ChatMaxStep: 20, MaxRetries: 1}})
	root := t.TempDir()
	if err := remotefs.SetLocalPolicy(remotefs.LocalPolicy{Roots: []string{root}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = remotefs.SetLocalPolicy(remotefs.LocalPolicy{})
		if old != nil {
			config.Update(old)
		}
	})
	session := &db.ScanSession{FilesystemID: 1, RootPath: ".", Status: "scanned", Goal: "照片按年份整理", AllowReadFile: true, AllowScript: true, AllowAutoCategory: true}
	if err := repo.CreateSession(session); err != nil {
		t.Fatal(err)
	}
	seedEntry(t, repo, db.FileEntry{ScanSessionID: session.ID, OriginalPath: "a.jpg", FileType: "file", ParentPath: ".", Size: 2048})
	a := New(repo, config.RemoteFSConfig{Protocol: "local", BasePath: root}, session.ID, claudeProvider(ws.srv.URL), session)
	return repo, a, session
}

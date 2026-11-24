package agent

import (
	"testing"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// wireChatAgent is an Agent whose model is the fake server (the real model
// factory), with one scanned file for the selection.
func wireChatAgent(t *testing.T, ws *wireServer) (*db.Repository, *Agent, *model.ScanSession) {
	t.Helper()
	repo := setupToolTestRepo(t)
	root := t.TempDir()
	if err := remotefs.SetLocalPolicy(remotefs.LocalPolicy{Roots: []string{root}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = remotefs.SetLocalPolicy(remotefs.LocalPolicy{})
	})
	session := &model.ScanSession{FilesystemID: 1, RootPath: ".", Status: "scanned", Goal: "照片按年份整理", AllowReadFile: true, AllowScript: true, AllowAutoCategory: true}
	if err := repo.CreateSession(session); err != nil {
		t.Fatal(err)
	}
	seedEntry(t, repo, model.FileEntry{ScanSessionID: session.ID, OriginalPath: "a.jpg", FileType: "file", ParentPath: ".", Size: 2048})
	a := New(repo, config.RemoteFSConfig{Protocol: "local", BasePath: root}, session.ID, claudeProvider(ws.srv.URL), session, testCfg(runCfg))
	return repo, a, session
}

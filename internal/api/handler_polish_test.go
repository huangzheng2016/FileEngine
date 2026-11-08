package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"FileEngine/internal/db"
)

// Error responses are {error: <Chinese>}; internal failures never leak the
// database error.
func TestErrorEnvelope(t *testing.T) {
	s := setupSessionTestServer(t)
	cases := []struct {
		method, path string
		body         any
		code         int
		msg          string
	}{
		{http.MethodGet, "/api/v1/sessions/abc", nil, http.StatusBadRequest, msgBadID},
		{http.MethodGet, "/api/v1/sessions/999", nil, http.StatusNotFound, msgNoSession},
		{http.MethodPost, "/api/v1/sessions", map[string]any{}, http.StatusBadRequest, msgBadBody},
		{http.MethodPost, "/api/v1/sessions", map[string]any{"filesystem_id": 999}, http.StatusBadRequest, msgNoFilesystem},
		{http.MethodGet, "/api/v1/models/999", nil, http.StatusNotFound, msgNoModel},
		{http.MethodGet, "/api/v1/files/999", nil, http.StatusNotFound, msgNoFile},
		{http.MethodGet, "/api/v1/runs/999", nil, http.StatusNotFound, msgNoRun},
		{http.MethodPost, "/api/v1/runs/999/revert", nil, http.StatusNotFound, msgNoRun},
		{http.MethodGet, "/api/v1/sessions/1/locate", nil, http.StatusBadRequest, "请输入要查找的文件名或路径"},
		{http.MethodPost, "/api/v1/sessions/1/execute", map[string]any{"mode": "zap"}, http.StatusBadRequest, "执行模式只能是 copy 或 move"},
		{http.MethodPost, "/api/v1/categories", map[string]any{"filesystem_id": 1, "name": "x"}, http.StatusBadRequest, msgBadBody},
	}
	for _, tc := range cases {
		code, resp := doModelRequest(t, s, tc.method, tc.path, tc.body)
		if code != tc.code || resp["error"] != tc.msg {
			t.Errorf("%s %s: %d %v; want %d %q", tc.method, tc.path, code, resp, tc.code, tc.msg)
		}
	}

	sqlDB, err := db.GetDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()
	code, resp := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions", nil)
	if code != http.StatusInternalServerError || resp["error"] != msgInternal || len(resp) != 1 {
		t.Fatalf("internal error: %d %v", code, resp)
	}
	code, resp = doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1", nil)
	if code != http.StatusInternalServerError || resp["error"] != msgInternal {
		t.Fatalf("lookup on a broken database must be 500, not 404: %d %v", code, resp)
	}
}

// A target equal to the entry's own path keeps it in place: the target is
// cleared and no correction is learned. User edits are marked 用户修改.
func TestFilePatchKeepInPlace(t *testing.T) {
	s, repo := brainServer(t)
	logo, _ := repo.GetFileByPath(1, "Design/logo.psd")
	if _, _, err := repo.SetTargetUnique(1, "Design/logo.psd", "/图片/logo.psd", "file"); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateFileFields(logo.ID, map[string]any{"reason": "扩展名规则"}); err != nil {
		t.Fatal(err)
	}

	url := fmt.Sprintf("/api/v1/files/%d", logo.ID)
	code, f := doModelRequest(t, s, http.MethodPatch, url, map[string]any{"new_path": "/Design//logo.psd"})
	if code != http.StatusOK || f["new_path"] != "" || f["operation"] != "" || f["reason"] != db.ReasonUserEdit {
		t.Fatalf("keep in place: %d %v", code, f)
	}
	if items, _ := repo.ListMemory(db.MemoryScopeFilesystem, nil); len(items) != 0 {
		t.Fatalf("keeping in place must learn nothing: %+v", items)
	}

	code, f = doModelRequest(t, s, http.MethodPatch, url, map[string]any{"new_path": "设计/logo.psd"})
	if code != http.StatusOK || f["new_path"] != "/设计/logo.psd" || f["reason"] != db.ReasonUserEdit {
		t.Fatalf("retarget: %d %v", code, f)
	}

	code, f = doModelRequest(t, s, http.MethodPatch, url, map[string]any{"new_path": "a/../../b"})
	if code != http.StatusBadRequest || !strings.Contains(f["error"].(string), "不能包含 ..") {
		t.Fatalf("illegal path: %d %v", code, f)
	}
}

// End to end through the API: check carries notes under a lower-case key,
// locate answers "/"-rooted targets, and reverting a move removes the
// directories the run created.
func TestExecuteRevertAndLocate(t *testing.T) {
	s := setupSessionTestServer(t)
	repo := s.repo
	fsRow, err := repo.GetFilesystem(1)
	if err != nil {
		t.Fatal(err)
	}
	base := fsRow.BasePath
	if err := os.MkdirAll(filepath.Join(base, "in"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "in", "a.txt"), []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(base, "in", "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	sess := &db.ScanSession{FilesystemID: 1, Status: "scanned"}
	if err := repo.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	f := &db.FileEntry{ScanSessionID: sess.ID, OriginalPath: "in/a.txt", Name: "a.txt", FileType: "file",
		ParentPath: "in", Size: st.Size(), ModTime: st.ModTime()}
	if err := repo.CreateFile(f); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.SetTargetUnique(sess.ID, "in/a.txt", "/docs/new/a.txt", "file"); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("/api/v1/sessions/%d", sess.ID)

	code, check := doModelRequest(t, s, http.MethodGet, prefix+"/execute/check?mode=move", nil)
	if code != http.StatusOK || check["total"] != float64(1) {
		t.Fatalf("check: %d %v", code, check)
	}
	if _, ok := check["Notes"]; ok {
		t.Fatalf("check must not carry an untagged Notes field: %v", check)
	}

	code, body := doModelRequest(t, s, http.MethodPost, prefix+"/execute", map[string]any{"mode": "move"})
	if code != http.StatusOK {
		t.Fatalf("execute: %d %v", code, body)
	}
	execRun := waitRun(t, repo, runIDOf(t, body))
	if execRun.Status != db.RunDone {
		t.Fatalf("execute run: %+v", execRun)
	}
	if _, err := os.Stat(filepath.Join(base, "docs", "new", "a.txt")); err != nil {
		t.Fatalf("not moved: %v", err)
	}

	code, loc := doModelRequest(t, s, http.MethodGet, prefix+"/locate?q=a.txt", nil)
	items, _ := loc["items"].([]any)
	if code != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["new_path"] != "/docs/new/a.txt" {
		t.Fatalf("locate: %d %v", code, loc)
	}

	code, body = doModelRequest(t, s, http.MethodPost, fmt.Sprintf("/api/v1/runs/%d/revert", execRun.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("revert: %d %v", code, body)
	}
	revRun := waitRun(t, repo, runIDOf(t, body))
	if revRun.Status != db.RunDone || !strings.Contains(revRun.Summary, "已删除执行时创建的 2 个空目录") {
		t.Fatalf("revert run: %+v", revRun)
	}
	if _, err := os.Stat(filepath.Join(base, "in", "a.txt")); err != nil {
		t.Fatalf("not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "docs")); !os.IsNotExist(err) {
		t.Fatalf("created directories left behind: %v", err)
	}

	code, body = doModelRequest(t, s, http.MethodPost, fmt.Sprintf("/api/v1/runs/%d/revert", revRun.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("revert of a revert run is an executing run too: %d %v", code, body)
	}
	waitRun(t, repo, runIDOf(t, body))
}

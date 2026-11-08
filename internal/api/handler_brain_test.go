package api

import (
	"fmt"
	"net/http"
	"testing"

	"FileEngine/internal/db"
)

func brainServer(t *testing.T) (*Server, *db.Repository) {
	t.Helper()
	s := setupSessionTestServer(t)
	repo := s.repo
	if err := repo.CreateSession(&db.ScanSession{FilesystemID: 1, RootPath: "", Status: "scanned"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []db.FileEntry{
		{ScanSessionID: 1, OriginalPath: "Design", Name: "Design", FileType: "directory", ParentPath: ""},
		{ScanSessionID: 1, OriginalPath: "Design/logo.psd", Name: "logo.psd", FileType: "file", ParentPath: "Design", Size: 10},
		{ScanSessionID: 1, OriginalPath: "Design/icon.psd", Name: "icon.psd", FileType: "file", ParentPath: "Design", Size: 20},
	} {
		e := e
		if err := repo.CreateFile(&e); err != nil {
			t.Fatal(err)
		}
	}
	return s, repo
}

func TestSessionGoalCreateAndPatch(t *testing.T) {
	s := setupSessionTestServer(t)
	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{
		"filesystem_id": 1, "goal": "  照片按年份整理 ",
	})
	if code != http.StatusCreated || created["goal"] != "照片按年份整理" {
		t.Fatalf("create: %d %v", code, created)
	}
	waitForScanDone(t, s, 1)
	code, patched := doModelRequest(t, s, http.MethodPatch, "/api/v1/sessions/1", map[string]any{"goal": "项目保持完整"})
	if code != http.StatusOK || patched["goal"] != "项目保持完整" {
		t.Fatalf("patch: %d %v", code, patched)
	}
	code, patched = doModelRequest(t, s, http.MethodPatch, "/api/v1/sessions/1", map[string]any{"filter_mode": "whitelist"})
	if code != http.StatusOK || patched["goal"] != "项目保持完整" {
		t.Fatalf("unrelated patch must keep the goal: %v", patched)
	}
}

func TestPlanEndpoints(t *testing.T) {
	s, _ := brainServer(t)

	code, plan := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1/plan", nil)
	if code != http.StatusOK || plan["version"].(float64) != 0 {
		t.Fatalf("empty plan: %d %v", code, plan)
	}
	if code, _ := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/99/plan", nil); code != http.StatusNotFound {
		t.Fatalf("unknown session: %d", code)
	}
	if code, _ := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/plan/approve", nil); code != http.StatusNotFound {
		t.Fatalf("approve without plan: %d", code)
	}
	code, plan = doModelRequest(t, s, http.MethodPut, "/api/v1/sessions/1/plan", map[string]any{"content": "# 方案", "version": 0})
	if code != http.StatusOK || plan["version"].(float64) != 1 || plan["status"] != db.PlanDraft {
		t.Fatalf("create: %d %v", code, plan)
	}
	code, conflict := doModelRequest(t, s, http.MethodPut, "/api/v1/sessions/1/plan", map[string]any{"content": "stale", "version": 0})
	if code != http.StatusConflict || conflict["plan"].(map[string]any)["content"] != "# 方案" {
		t.Fatalf("stale edit: %d %v", code, conflict)
	}
	code, plan = doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/plan/approve", nil)
	if code != http.StatusOK || plan["status"] != db.PlanApproved || plan["approved_at"] == nil {
		t.Fatalf("approve: %d %v", code, plan)
	}
	code, plan = doModelRequest(t, s, http.MethodPut, "/api/v1/sessions/1/plan", map[string]any{"content": "# 方案 v2", "version": 1})
	if code != http.StatusOK || plan["version"].(float64) != 2 {
		t.Fatalf("edit: %d %v", code, plan)
	}
}

func TestMemoryEndpoints(t *testing.T) {
	s, _ := brainServer(t)

	code, m := doModelRequest(t, s, http.MethodPost, "/api/v1/memory", map[string]any{
		"scope": "global", "kind": "preference", "key": "pref:lang", "content": "文件夹用中文名",
	})
	if code != http.StatusCreated || m["source"] != db.MemorySourceUser {
		t.Fatalf("create: %d %v", code, m)
	}
	id := uint(m["id"].(float64))
	code, m = doModelRequest(t, s, http.MethodPost, "/api/v1/memory", map[string]any{
		"scope": "global", "kind": "preference", "key": "PREF:LANG", "content": "文件夹用中文名，英文缩写保留",
	})
	if code != http.StatusOK || uint(m["id"].(float64)) != id {
		t.Fatalf("same key upserts: %d %v", code, m)
	}
	if code, _ := doModelRequest(t, s, http.MethodPost, "/api/v1/memory", map[string]any{"scope": "global", "kind": "x", "key": "k", "content": "c"}); code != http.StatusBadRequest {
		t.Fatalf("invalid kind: %d", code)
	}
	if _, err := s.repo.UpsertMemory(&db.MemoryItem{Scope: db.MemoryScopeFilesystem, ScopeID: 1, Kind: db.MemoryFact, Key: "f", Content: "fs fact"}); err != nil {
		t.Fatal(err)
	}

	code, list := doModelRequest(t, s, http.MethodGet, "/api/v1/memory?scope=filesystem&scope_id=1", nil)
	if code != http.StatusOK || len(list["items"].([]any)) != 1 {
		t.Fatalf("list scoped: %d %v", code, list)
	}
	_, list = doModelRequest(t, s, http.MethodGet, "/api/v1/memory", nil)
	if len(list["items"].([]any)) != 2 {
		t.Fatalf("list all: %v", list)
	}
	if code, _ := doModelRequest(t, s, http.MethodGet, "/api/v1/memory?scope=bad", nil); code != http.StatusBadRequest {
		t.Fatal("bad scope")
	}

	code, m = doModelRequest(t, s, http.MethodPut, fmt.Sprintf("/api/v1/memory/%d", id), map[string]any{"pinned": true, "content": "目录用中文"})
	if code != http.StatusOK || m["pinned"] != true || m["content"] != "目录用中文" || m["key"] != "pref:lang" {
		t.Fatalf("update: %d %v", code, m)
	}
	if code, _ := doModelRequest(t, s, http.MethodPut, "/api/v1/memory/999", map[string]any{"pinned": true}); code != http.StatusNotFound {
		t.Fatalf("update missing: %d", code)
	}
	if code, _ := doModelRequest(t, s, http.MethodDelete, fmt.Sprintf("/api/v1/memory/%d", id), nil); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := doModelRequest(t, s, http.MethodDelete, fmt.Sprintf("/api/v1/memory/%d", id), nil); code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", code)
	}
}

func TestFilePatchCapturesCorrection(t *testing.T) {
	s, repo := brainServer(t)
	logo, _ := repo.GetFileByPath(1, "Design/logo.psd")
	icon, _ := repo.GetFileByPath(1, "Design/icon.psd")
	if _, _, err := repo.SetTargetUnique(1, "Design/logo.psd", "/图片/logo.psd", "file"); err != nil {
		t.Fatal(err)
	}

	code, f := doModelRequest(t, s, http.MethodPatch, fmt.Sprintf("/api/v1/files/%d", logo.ID), map[string]any{"new_path": "设计/源文件/logo.psd"})
	if code != http.StatusOK || f["new_path"] != "/设计/源文件/logo.psd" || f["operation"] != "planned" {
		t.Fatalf("patch: %d %v", code, f)
	}
	items, _ := repo.ListMemory(db.MemoryScopeFilesystem, nil)
	if len(items) != 1 || items[0].Kind != db.MemoryCorrection || items[0].ScopeID != 1 || items[0].Key != "correction:.psd@/design" ||
		items[0].Content != "`/Design` 下的 `*.psd` 应放到 `/设计/源文件`，而不是 `/图片`" {
		t.Fatalf("correction memory: %+v", items)
	}

	code, f = doModelRequest(t, s, http.MethodPatch, fmt.Sprintf("/api/v1/files/%d", icon.ID), map[string]any{"new_path": "/设计/源文件/logo.psd", "description": "图标"})
	if code != http.StatusOK || f["new_path"] != "/设计/源文件/logo (2).psd" || f["description"] != "图标" {
		t.Fatalf("collision: %d %v", code, f)
	}
	if items, _ := repo.ListMemory(db.MemoryScopeFilesystem, nil); len(items) != 1 {
		t.Fatalf("one correction per key: %+v", items)
	}

	if code, _ := doModelRequest(t, s, http.MethodPatch, fmt.Sprintf("/api/v1/files/%d", icon.ID), map[string]any{"new_path": "/../escape"}); code != http.StatusBadRequest {
		t.Fatalf("illegal path: %d", code)
	}
	code, f = doModelRequest(t, s, http.MethodPatch, fmt.Sprintf("/api/v1/files/%d", logo.ID), map[string]any{"description": "标志"})
	if code != http.StatusOK || f["new_path"] != "/设计/源文件/logo.psd" {
		t.Fatalf("description only: %v", f)
	}
}

func TestPreviewAndResetEndpoints(t *testing.T) {
	s, repo := brainServer(t)
	logo, _ := repo.GetFileByPath(1, "Design/logo.psd")
	if _, _, err := repo.SetTargetUnique(1, "Design/logo.psd", "/设计/logo.psd", "file"); err != nil {
		t.Fatal(err)
	}

	code, after := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1/preview/tree?side=after", nil)
	items := after["items"].([]any)
	if code != http.StatusOK || after["total"].(float64) != 1 || items[0].(map[string]any)["name"] != "设计" {
		t.Fatalf("after root: %d %v", code, after)
	}
	_, after = doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1/preview/tree?side=after&path=/设计", nil)
	leaf := after["items"].([]any)[0].(map[string]any)
	if leaf["file_id"].(float64) != float64(logo.ID) || leaf["original_path"] != "Design/logo.psd" {
		t.Fatalf("after leaf: %v", leaf)
	}
	code, before := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1/preview/tree?side=before", nil)
	root := before["items"].([]any)[0].(map[string]any)
	if code != http.StatusOK || root["status"] != db.PreviewPartial || root["moved_count"].(float64) != 1 || root["file_count"].(float64) != 2 {
		t.Fatalf("before root: %d %v", code, before)
	}
	if code, _ := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1/preview/tree?side=sideways", nil); code != http.StatusBadRequest {
		t.Fatalf("bad side: %d", code)
	}

	if code, _ := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/plans/reset", map[string]any{"file_ids": []uint{}}); code != http.StatusBadRequest {
		t.Fatalf("empty reset: %d", code)
	}
	code, res := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/plans/reset", map[string]any{"file_ids": []uint{logo.ID}})
	if code != http.StatusOK || res["reset"].(float64) != 1 {
		t.Fatalf("reset: %d %v", code, res)
	}
	f, _ := repo.GetFile(logo.ID)
	if f.NewPath != "" || f.Tagged {
		t.Fatalf("reset row: %+v", f)
	}
}

func TestDeleteSessionDropsPlanAndNotes(t *testing.T) {
	s, repo := brainServer(t)
	if _, err := repo.SavePlan(1, "# 方案", 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertMemory(&db.MemoryItem{Scope: db.MemoryScopeSession, ScopeID: 1, Kind: db.MemoryNote, Key: "n", Content: "笔记"}); err != nil {
		t.Fatal(err)
	}
	if code, _ := doModelRequest(t, s, http.MethodDelete, "/api/v1/sessions/1", nil); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	if has, _ := repo.HasPlan(1); has {
		t.Fatal("plan must be deleted")
	}
	if notes, _ := repo.NotesSince(1, 0, 0); len(notes) != 0 {
		t.Fatal("notes must be deleted")
	}
}

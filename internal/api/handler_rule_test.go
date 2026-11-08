package api

import (
	"fmt"
	"net/http"
	"testing"

	"FileEngine/internal/db"
)

func TestRuleEndpoints(t *testing.T) {
	s, repo := brainServer(t)
	code, rule := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/rules", map[string]any{
		"name": "设计稿", "exts": "psd", "target": "设计/{year}", "role": "file",
	})
	if code != http.StatusCreated || rule["position"].(float64) != 1 || rule["source"] != "user" {
		t.Fatalf("create: %d %v", code, rule)
	}
	id := uint(rule["id"].(float64))
	if code, _ := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/rules", map[string]any{"name": "x", "target": "a/{bad}"}); code != http.StatusBadRequest {
		t.Fatalf("invalid template: %d", code)
	}
	code, list := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1/rules", nil)
	if code != http.StatusOK || len(list["items"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, list)
	}
	code, upd := doModelRequest(t, s, http.MethodPut, fmt.Sprintf("/api/v1/rules/%d", id), map[string]any{"position": 3})
	if code != http.StatusOK || upd["position"].(float64) != 3 || upd["exts"] != "psd" {
		t.Fatalf("partial update: %d %v", code, upd)
	}

	code, pv := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/rules/apply?preview=1", nil)
	items := pv["items"].([]any)
	first := items[0].(map[string]any)
	sample := first["samples"].([]any)[0].(map[string]any)
	if code != http.StatusOK || first["matched"].(float64) != 2 || sample["original_path"] == nil || sample["new_path"] == nil {
		t.Fatalf("preview: %d %v", code, pv)
	}
	if e, _ := repo.GetFileByPath(1, "Design/logo.psd"); e.Role != db.RoleUndecided {
		t.Fatal("preview writes nothing")
	}
	code, ap := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/rules/apply", nil)
	if code != http.StatusOK || ap["applied"].(float64) != 2 {
		t.Fatalf("apply: %d %v", code, ap)
	}
	if e, _ := repo.GetFileByPath(1, "Design/logo.psd"); e.Reason != "规则：设计稿" || e.Role != db.RoleFile {
		t.Fatalf("applied: %+v", e)
	}
	if code, _ := doModelRequest(t, s, http.MethodDelete, fmt.Sprintf("/api/v1/rules/%d", id), nil); code != http.StatusOK {
		t.Fatal("delete")
	}
	if code, _ := doModelRequest(t, s, http.MethodDelete, fmt.Sprintf("/api/v1/rules/%d", id), nil); code != http.StatusNotFound {
		t.Fatal("delete twice")
	}
}

func TestReviewConfirmAndFileFilters(t *testing.T) {
	s, repo := brainServer(t)
	res, err := repo.Decide(1, db.Decision{Path: "Design/logo.psd", Role: db.RoleFile, NewPath: "/设计/logo.psd", Reason: "猜的", Confidence: 0.3})
	if err != nil || !res.NeedsReview {
		t.Fatal(err)
	}
	logo, _ := repo.GetFileByPath(1, "Design/logo.psd")

	code, list := doModelRequest(t, s, http.MethodGet, "/api/v1/files?session_id=1&needs_review=true", nil)
	files := list["files"].([]any)
	if code != http.StatusOK || len(files) != 1 {
		t.Fatalf("needs_review filter: %v", list)
	}
	f := files[0].(map[string]any)
	if f["role"] != "file" || f["reason"] != "猜的" || f["confidence"].(float64) != 0.3 || f["sensitive"] != false {
		t.Fatalf("entry json: %v", f)
	}
	_, list = doModelRequest(t, s, http.MethodGet, "/api/v1/files?session_id=1&role=", nil)
	if len(list["files"].([]any)) != 2 {
		t.Fatalf("role= (undecided) filter: %v", list)
	}

	code, conf := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/review/confirm", map[string]any{"file_ids": []uint{logo.ID}})
	if code != http.StatusOK || conf["confirmed"].(float64) != 1 {
		t.Fatalf("confirm: %d %v", code, conf)
	}
	if code, _ := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/review/confirm", map[string]any{}); code != http.StatusBadRequest {
		t.Fatal("empty confirm")
	}

	// Manual edits under a unit conflict with the decision model.
	if _, err := repo.Decide(1, db.Decision{Path: "Design", Role: db.RoleUnit, NewPath: "/设计/Design", Override: true}); err != nil {
		t.Fatal(err)
	}
	code, body := doModelRequest(t, s, http.MethodPatch, fmt.Sprintf("/api/v1/files/%d", logo.ID), map[string]any{"new_path": "/设计/x.psd"})
	if code != http.StatusConflict {
		t.Fatalf("PATCH under unit: %d %v", code, body)
	}
	_, tree := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1/preview/tree?side=after&path=/设计", nil)
	item := tree["items"].([]any)[0].(map[string]any)
	if item["role"] != "unit" {
		t.Fatalf("preview carries role: %v", item)
	}
}

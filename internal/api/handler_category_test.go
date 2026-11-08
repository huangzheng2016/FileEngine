package api

import (
	"net/http"
	"strings"
	"testing"

	"FileEngine/internal/db"
)

func TestCategoryPathValidationREST(t *testing.T) {
	s := setupSessionTestServer(t)

	code, resp := doModelRequest(t, s, http.MethodPost, "/api/v1/categories", map[string]any{
		"filesystem_id": 1, "name": "Bad", "path": "/../evil",
	})
	if code != http.StatusBadRequest || !strings.Contains(resp["error"].(string), "分类路径非法") {
		t.Fatalf("create evil path: code %d, resp %v", code, resp)
	}
	if cats, _ := db.NewRepository(db.GetDB()).ListCategories(1); len(cats) != 0 {
		t.Fatalf("rejected create must write nothing, got %d categories", len(cats))
	}

	code, _ = doModelRequest(t, s, http.MethodPost, "/api/v1/categories", map[string]any{
		"filesystem_id": 1, "name": "Good", "path": "/organized/good",
	})
	if code != http.StatusCreated {
		t.Fatalf("create valid: code %d", code)
	}

	code, resp = doModelRequest(t, s, http.MethodPut, "/api/v1/categories/1", map[string]any{
		"filesystem_id": 1, "name": "Good", "path": "/a/../../b",
	})
	if code != http.StatusBadRequest || !strings.Contains(resp["error"].(string), "分类路径非法") {
		t.Fatalf("update evil path: code %d, resp %v", code, resp)
	}
	cat, err := db.NewRepository(db.GetDB()).GetCategory(1)
	if err != nil || cat.Path != "/organized/good" {
		t.Fatalf("rejected update must not change path: cat %+v, err %v", cat, err)
	}

	code, _ = doModelRequest(t, s, http.MethodPut, "/api/v1/categories/1", map[string]any{
		"filesystem_id": 1, "name": "Good", "path": "/organized/better",
	})
	if code != http.StatusOK {
		t.Fatalf("update valid: code %d", code)
	}

	code, resp = doModelRequest(t, s, http.MethodPost, "/api/v1/categories/import?filesystem_id=1", []map[string]any{
		{"name": "Imported", "path": "/imported"},
		{"name": "Evil", "path": "/../evil"},
		{"name": "NoPath"},
	})
	if code != http.StatusOK {
		t.Fatalf("import: code %d, resp %v", code, resp)
	}
	if got := resp["created"].(float64); got != 1 {
		t.Fatalf("import created: got %v, want 1", got)
	}
	if got := resp["skipped"].(float64); got != 2 {
		t.Fatalf("import skipped: got %v, want 2 (evil path + empty path)", got)
	}
	if _, err := db.NewRepository(db.GetDB()).GetCategoryByName(1, "Imported"); err != nil {
		t.Fatalf("legal import item must be created: %v", err)
	}
	if _, err := db.NewRepository(db.GetDB()).GetCategoryByName(1, "Evil"); err == nil {
		t.Fatal("illegal import item must not be created")
	}
}

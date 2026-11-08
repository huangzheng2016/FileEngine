package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func setupModelTestServer(t *testing.T) *Server {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := db.Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("db init: %v", err)
	}
	return NewServer(db.NewRepository(db.GetDB()), nil)
}

func doModelRequest(t *testing.T, s *Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveAuthed(s, w, req)
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return w.Code, resp
}

func TestModelProviderContextWindow(t *testing.T) {
	s := setupModelTestServer(t)

	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "plain", "provider": "openai", "model": "gpt-4o", "max_tokens": 4096,
	})
	if code != http.StatusCreated {
		t.Fatalf("create plain: code %d, resp %v", code, created)
	}
	if got := created["context_window"]; got != float64(db.DefaultContextWindow) {
		t.Fatalf("plain create context_window: got %v, want %d", got, db.DefaultContextWindow)
	}
	if got := created["max_tokens"]; got != float64(4096) {
		t.Fatalf("plain create max_tokens: got %v, want 4096", got)
	}
	id := created["id"]

	code, created = doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "custom", "provider": "claude", "model": "claude-sonnet-4", "context_window": 500000,
	})
	if code != http.StatusCreated {
		t.Fatalf("create custom: code %d, resp %v", code, created)
	}
	if got := created["context_window"]; got != float64(500000) {
		t.Fatalf("custom create context_window: got %v, want 500000", got)
	}

	code, updated := doModelRequest(t, s, http.MethodPut, "/api/v1/models/2", map[string]any{
		"name": "custom", "provider": "claude", "model": "claude-sonnet-4", "max_tokens": 8192,
	})
	if code != http.StatusOK {
		t.Fatalf("update without context_window: code %d, resp %v", code, updated)
	}
	if got := updated["context_window"]; got != float64(500000) {
		t.Fatalf("context_window clobbered by update: got %v, want 500000", got)
	}

	code, updated = doModelRequest(t, s, http.MethodPut, "/api/v1/models/2", map[string]any{
		"name": "custom", "provider": "claude", "model": "claude-sonnet-4", "context_window": 1000000,
	})
	if code != http.StatusOK {
		t.Fatalf("update with context_window: code %d, resp %v", code, updated)
	}
	if got := updated["context_window"]; got != float64(1000000) {
		t.Fatalf("update context_window: got %v, want 1000000", got)
	}

	code, _ = doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "bad", "provider": "openai", "model": "gpt-4o", "context_window": -1,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("negative context_window: got code %d, want 400", code)
	}

	code, got := doModelRequest(t, s, http.MethodGet, "/api/v1/models/1", nil)
	if code != http.StatusOK {
		t.Fatalf("get: code %d", code)
	}
	if got["id"] != id {
		t.Fatalf("get id mismatch: %v vs %v", got["id"], id)
	}
	if v := got["context_window"]; v != float64(db.DefaultContextWindow) {
		t.Fatalf("get context_window: got %v, want %d", v, db.DefaultContextWindow)
	}
}

func TestModelProviderReasoningEffortPassthrough(t *testing.T) {
	s := setupModelTestServer(t)

	for _, v := range []string{"", "none", "low", "medium", "high", "max", "xhigh_v2-9", "X-High_2"} {
		code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
			"name": "effort-" + v, "provider": "openai", "model": "gpt-4o", "reasoning_effort": v,
		})
		if code != http.StatusCreated {
			t.Fatalf("create reasoning_effort=%q: code %d, resp %v", v, code, created)
		}
		if got := created["reasoning_effort"]; got != v {
			t.Fatalf("create reasoning_effort=%q: stored %v", v, got)
		}
	}

	code, updated := doModelRequest(t, s, http.MethodPut, "/api/v1/models/1", map[string]any{
		"name": "effort-", "provider": "openai", "model": "gpt-4o", "reasoning_effort": "max",
	})
	if code != http.StatusOK {
		t.Fatalf("update to max: code %d, resp %v", code, updated)
	}
	if got := updated["reasoning_effort"]; got != "max" {
		t.Fatalf("update to max: stored %v", got)
	}

	bad := []string{"has space", "bad!chars", "chinese中文", "this-value-is-way-too-long-to-pass32"}
	for _, v := range bad {
		code, _ = doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
			"name": "bad", "provider": "openai", "model": "gpt-4o", "reasoning_effort": v,
		})
		if code != http.StatusBadRequest {
			t.Fatalf("create reasoning_effort=%q: got code %d, want 400", v, code)
		}
		code, _ = doModelRequest(t, s, http.MethodPut, "/api/v1/models/1", map[string]any{
			"name": "effort-", "provider": "openai", "model": "gpt-4o", "reasoning_effort": v,
		})
		if code != http.StatusBadRequest {
			t.Fatalf("update reasoning_effort=%q: got code %d, want 400", v, code)
		}
	}
	code, got := doModelRequest(t, s, http.MethodGet, "/api/v1/models/1", nil)
	if code != http.StatusOK {
		t.Fatalf("get: code %d", code)
	}
	if got["reasoning_effort"] != "max" {
		t.Fatalf("stored reasoning_effort after rejected updates: got %v, want max", got["reasoning_effort"])
	}

	code, resp := doModelRequest(t, s, http.MethodPost, "/api/v1/models/test", map[string]any{
		"name": "t", "provider": "openai", "model": "gpt-4o", "reasoning_effort": "has space",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("test path invalid effort: got code %d, want 400 (resp %v)", code, resp)
	}
}

func TestModelProviderUpdateRejectsNonPositiveContextWindow(t *testing.T) {
	s := setupModelTestServer(t)

	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "cw", "provider": "openai", "model": "gpt-4o", "context_window": 256000,
	})
	if code != http.StatusCreated {
		t.Fatalf("create: code %d, resp %v", code, created)
	}

	for _, v := range []int{0, -5} {
		code, _ = doModelRequest(t, s, http.MethodPut, "/api/v1/models/1", map[string]any{
			"name": "cw", "provider": "openai", "model": "gpt-4o", "context_window": v,
		})
		if code != http.StatusBadRequest {
			t.Fatalf("update context_window=%d: got code %d, want 400", v, code)
		}
	}

	code, got := doModelRequest(t, s, http.MethodGet, "/api/v1/models/1", nil)
	if code != http.StatusOK {
		t.Fatalf("get: code %d", code)
	}
	if v := got["context_window"]; v != float64(256000) {
		t.Fatalf("stored context_window after rejected updates: got %v, want 256000", v)
	}
}

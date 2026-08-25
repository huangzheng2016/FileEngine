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
	s.Router().ServeHTTP(w, req)
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return w.Code, resp
}

// Old clients send no context_window; new clients may pass tier/custom values.
func TestModelProviderContextWindowCompat(t *testing.T) {
	s := setupModelTestServer(t)

	// Create without context_window (legacy client) -> default applied.
	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "legacy", "provider": "openai", "model": "gpt-4o", "max_tokens": 4096,
	})
	if code != http.StatusCreated {
		t.Fatalf("create legacy: code %d, resp %v", code, created)
	}
	if got := created["context_window"]; got != float64(db.DefaultContextWindow) {
		t.Fatalf("legacy create context_window: got %v, want %d", got, db.DefaultContextWindow)
	}
	if got := created["max_tokens"]; got != float64(4096) {
		t.Fatalf("legacy create max_tokens: got %v, want 4096", got)
	}
	id := created["id"]

	// Create with an explicit custom value.
	code, created = doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "custom", "provider": "claude", "model": "claude-sonnet-4", "context_window": 500000,
	})
	if code != http.StatusCreated {
		t.Fatalf("create custom: code %d, resp %v", code, created)
	}
	if got := created["context_window"]; got != float64(500000) {
		t.Fatalf("custom create context_window: got %v, want 500000", got)
	}

	// Full-form update WITHOUT context_window (legacy client) must not clobber it.
	code, updated := doModelRequest(t, s, http.MethodPut, "/api/v1/models/2", map[string]any{
		"name": "custom", "provider": "claude", "model": "claude-sonnet-4", "max_tokens": 8192,
	})
	if code != http.StatusOK {
		t.Fatalf("update legacy-form: code %d, resp %v", code, updated)
	}
	if got := updated["context_window"]; got != float64(500000) {
		t.Fatalf("context_window clobbered by legacy update: got %v, want 500000", got)
	}

	// Update with context_window.
	code, updated = doModelRequest(t, s, http.MethodPut, "/api/v1/models/2", map[string]any{
		"name": "custom", "provider": "claude", "model": "claude-sonnet-4", "context_window": 1000000,
	})
	if code != http.StatusOK {
		t.Fatalf("update with context_window: code %d, resp %v", code, updated)
	}
	if got := updated["context_window"]; got != float64(1000000) {
		t.Fatalf("update context_window: got %v, want 1000000", got)
	}

	// Negative values are rejected.
	code, _ = doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "bad", "provider": "openai", "model": "gpt-4o", "context_window": -1,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("negative context_window: got code %d, want 400", code)
	}

	// Legacy row is still retrievable and reports the effective window.
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

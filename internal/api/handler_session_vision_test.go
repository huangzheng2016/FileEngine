package api

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func setupSessionTestServer(t *testing.T) *Server {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := db.Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("db init: %v", err)
	}
	repo := db.NewRepository(db.GetDB())
	if err := repo.CreateFilesystem(&db.Filesystem{
		Name: "local", Protocol: "local", BasePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("create filesystem: %v", err)
	}
	if err := repo.CreateModelProvider(&db.ModelProvider{Name: "fake", Provider: "openai", Model: "fake"}); err != nil {
		t.Fatalf("create model: %v", err)
	}
	return NewServer(repo, nil)
}

func waitForScanDone(t *testing.T, s *Server, id uint) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		session, err := db.NewRepository(db.GetDB()).GetSession(id)
		if err != nil {
			t.Fatalf("get session %d: %v", id, err)
		}
		if session.Status != "scanning" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %d still scanning after deadline (status matters for test determinism)", id)
}

func TestSessionAllowVisionToggle(t *testing.T) {
	s := setupSessionTestServer(t)

	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{
		"filesystem_id": 1, "model_provider_id": 1,
	})
	if code != http.StatusCreated {
		t.Fatalf("create session: code %d, resp %v", code, created)
	}
	if got, ok := created["allow_vision"].(bool); !ok || got {
		t.Fatalf("create allow_vision: got %v, want false", created["allow_vision"])
	}

	// The background scan writes the session row; wait so later PATCH assertions
	// do not race it.
	waitForScanDone(t, s, 1)

	code, patched := doModelRequest(t, s, http.MethodPatch, "/api/v1/sessions/1", map[string]any{
		"allow_vision": true,
	})
	if code != http.StatusOK {
		t.Fatalf("patch allow_vision: code %d, resp %v", code, patched)
	}
	if got, ok := patched["allow_vision"].(bool); !ok || !got {
		t.Fatalf("patch allow_vision: got %v, want true", patched["allow_vision"])
	}

	code, patched = doModelRequest(t, s, http.MethodPatch, "/api/v1/sessions/1", map[string]any{
		"filter_mode": "whitelist",
	})
	if code != http.StatusOK {
		t.Fatalf("patch filter_mode: code %d, resp %v", code, patched)
	}
	if got, ok := patched["allow_vision"].(bool); !ok || !got {
		t.Fatalf("allow_vision clobbered by unrelated patch: got %v, want true", patched["allow_vision"])
	}

	code, got := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1", nil)
	if code != http.StatusOK {
		t.Fatalf("get session: code %d", code)
	}
	if v, ok := got["allow_vision"].(bool); !ok || !v {
		t.Fatalf("get allow_vision: got %v, want true", got["allow_vision"])
	}

	code, patched = doModelRequest(t, s, http.MethodPatch, "/api/v1/sessions/1", map[string]any{
		"allow_vision": false,
	})
	if code != http.StatusOK {
		t.Fatalf("patch off: code %d, resp %v", code, patched)
	}
	if v, ok := patched["allow_vision"].(bool); !ok || v {
		t.Fatalf("patch off allow_vision: got %v, want false", patched["allow_vision"])
	}
}

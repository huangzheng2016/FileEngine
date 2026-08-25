package api

import (
	"net/http"
	"testing"
)

// vision_enabled is additive: old clients omit it (create -> false, update ->
// keeps stored value); new clients can toggle it explicitly.
func TestModelProviderVisionEnabledCompat(t *testing.T) {
	s := setupModelTestServer(t)

	// Create without vision_enabled (legacy client) -> defaults to false.
	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "plain", "provider": "openai", "model": "gpt-4o",
	})
	if code != http.StatusCreated {
		t.Fatalf("create legacy: code %d, resp %v", code, created)
	}
	if got, ok := created["vision_enabled"].(bool); !ok || got {
		t.Fatalf("legacy create vision_enabled: got %v, want false", created["vision_enabled"])
	}

	// Create with vision_enabled: true.
	code, created = doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "vision", "provider": "claude", "model": "claude-sonnet-4", "vision_enabled": true,
	})
	if code != http.StatusCreated {
		t.Fatalf("create vision: code %d, resp %v", code, created)
	}
	if got, ok := created["vision_enabled"].(bool); !ok || !got {
		t.Fatalf("create vision_enabled: got %v, want true", created["vision_enabled"])
	}

	// Full-form update WITHOUT vision_enabled (legacy client) must not clobber it.
	code, updated := doModelRequest(t, s, http.MethodPut, "/api/v1/models/2", map[string]any{
		"name": "vision", "provider": "claude", "model": "claude-sonnet-4", "max_tokens": 8192,
	})
	if code != http.StatusOK {
		t.Fatalf("update legacy-form: code %d, resp %v", code, updated)
	}
	if got, ok := updated["vision_enabled"].(bool); !ok || !got {
		t.Fatalf("vision_enabled clobbered by legacy update: got %v, want true", updated["vision_enabled"])
	}

	// Explicit toggle off via update.
	code, updated = doModelRequest(t, s, http.MethodPut, "/api/v1/models/2", map[string]any{
		"name": "vision", "provider": "claude", "model": "claude-sonnet-4", "vision_enabled": false,
	})
	if code != http.StatusOK {
		t.Fatalf("update vision_enabled: code %d, resp %v", code, updated)
	}
	if got, ok := updated["vision_enabled"].(bool); !ok || got {
		t.Fatalf("update vision_enabled: got %v, want false", updated["vision_enabled"])
	}
}

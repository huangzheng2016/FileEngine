package api

import (
	"net/http"
	"testing"
)

func TestModelProviderSwitches(t *testing.T) {
	s := setupModelTestServer(t)

	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "plain", "provider": "openai", "model": "gpt-4o",
	})
	if code != http.StatusCreated {
		t.Fatalf("create plain: code %d, resp %v", code, created)
	}
	for _, k := range []string{"vision_enabled", "thinking_enabled"} {
		if got, ok := created[k].(bool); !ok || got {
			t.Fatalf("create without %s: got %v, want false", k, created[k])
		}
	}

	code, created = doModelRequest(t, s, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "smart", "provider": "claude", "model": "claude-sonnet-4",
		"vision_enabled": true, "thinking_enabled": true, "reasoning_effort": "high",
	})
	if code != http.StatusCreated {
		t.Fatalf("create smart: code %d, resp %v", code, created)
	}
	if created["vision_enabled"] != true || created["thinking_enabled"] != true || created["reasoning_effort"] != "high" {
		t.Fatalf("create smart: %v", created)
	}

	code, updated := doModelRequest(t, s, http.MethodPut, "/api/v1/models/2", map[string]any{
		"name": "smart", "provider": "claude", "model": "claude-sonnet-4", "max_tokens": 8192,
	})
	if code != http.StatusOK {
		t.Fatalf("update: code %d, resp %v", code, updated)
	}
	if updated["vision_enabled"] != true || updated["thinking_enabled"] != true || updated["reasoning_effort"] != "high" {
		t.Fatalf("switches clobbered by update without them: %v", updated)
	}

	code, updated = doModelRequest(t, s, http.MethodPut, "/api/v1/models/2", map[string]any{
		"name": "smart", "provider": "claude", "model": "claude-sonnet-4",
		"vision_enabled": false, "thinking_enabled": false,
	})
	if code != http.StatusOK {
		t.Fatalf("toggle off: code %d, resp %v", code, updated)
	}
	if updated["vision_enabled"] != false || updated["thinking_enabled"] != false {
		t.Fatalf("toggle off: %v", updated)
	}
}

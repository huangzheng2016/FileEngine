package api

import (
	"net/http"
	"testing"
)

// allow_auto_category follows the same per-session pattern as allow_script:
// the default is ON (DB default, owner decision): created on, toggleable via
// PATCH, untouched by unrelated patches.
func TestSessionAllowAutoCategoryToggle(t *testing.T) {
	s := setupSessionTestServer(t)

	// Create -> allow_auto_category defaults to true.
	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{
		"filesystem_id": 1,
	})
	if code != http.StatusCreated {
		t.Fatalf("create session: code %d, resp %v", code, created)
	}
	if got, ok := created["allow_auto_category"].(bool); !ok || !got {
		t.Fatalf("create allow_auto_category: got %v, want true", created["allow_auto_category"])
	}

	// Creating a session kicks off a background scan that writes the session
	// row (stale struct). Wait for it to finish so later PATCH assertions are
	// not racing its writes.
	waitForScanDone(t, s, 1)

	// Toggle off.
	code, patched := doModelRequest(t, s, http.MethodPatch, "/api/v1/sessions/1", map[string]any{
		"allow_auto_category": false,
	})
	if code != http.StatusOK {
		t.Fatalf("patch allow_auto_category: code %d, resp %v", code, patched)
	}
	if got, ok := patched["allow_auto_category"].(bool); !ok || got {
		t.Fatalf("patch allow_auto_category: got %v, want false", patched["allow_auto_category"])
	}

	// Unrelated patch must not clobber it.
	code, patched = doModelRequest(t, s, http.MethodPatch, "/api/v1/sessions/1", map[string]any{
		"filter_mode": "whitelist",
	})
	if code != http.StatusOK {
		t.Fatalf("patch filter_mode: code %d, resp %v", code, patched)
	}
	if got, ok := patched["allow_auto_category"].(bool); !ok || got {
		t.Fatalf("allow_auto_category clobbered by unrelated patch: got %v, want false", patched["allow_auto_category"])
	}

	// GET reflects the stored value.
	code, got := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1", nil)
	if code != http.StatusOK {
		t.Fatalf("get session: code %d", code)
	}
	if v, ok := got["allow_auto_category"].(bool); !ok || v {
		t.Fatalf("get allow_auto_category: got %v, want false", got["allow_auto_category"])
	}

	// Toggle back on.
	code, patched = doModelRequest(t, s, http.MethodPatch, "/api/v1/sessions/1", map[string]any{
		"allow_auto_category": true,
	})
	if code != http.StatusOK {
		t.Fatalf("patch on: code %d, resp %v", code, patched)
	}
	if v, ok := patched["allow_auto_category"].(bool); !ok || !v {
		t.Fatalf("patch on allow_auto_category: got %v, want true", patched["allow_auto_category"])
	}
}

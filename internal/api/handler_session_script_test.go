package api

import (
	"net/http"
	"testing"
)

func TestSessionAllowScriptToggle(t *testing.T) {
	s := setupSessionTestServer(t)

	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{
		"filesystem_id": 1,
	})
	if code != http.StatusCreated {
		t.Fatalf("create session: code %d, resp %v", code, created)
	}
	if got, ok := created["allow_script"].(bool); !ok || !got {
		t.Fatalf("create allow_script: got %v, want true", created["allow_script"])
	}

	// The background scan writes the session row; wait so later PATCH assertions
	// do not race it.
	waitForScanDone(t, s, 1)

	code, patched := doModelRequest(t, s, http.MethodPatch, "/api/v1/sessions/1", map[string]any{
		"allow_script": false,
	})
	if code != http.StatusOK {
		t.Fatalf("patch allow_script: code %d, resp %v", code, patched)
	}
	if got, ok := patched["allow_script"].(bool); !ok || got {
		t.Fatalf("patch allow_script: got %v, want false", patched["allow_script"])
	}

	code, patched = doModelRequest(t, s, http.MethodPatch, "/api/v1/sessions/1", map[string]any{
		"filter_mode": "whitelist",
	})
	if code != http.StatusOK {
		t.Fatalf("patch filter_mode: code %d, resp %v", code, patched)
	}
	if got, ok := patched["allow_script"].(bool); !ok || got {
		t.Fatalf("allow_script clobbered by unrelated patch: got %v, want false", patched["allow_script"])
	}

	code, got := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1", nil)
	if code != http.StatusOK {
		t.Fatalf("get session: code %d", code)
	}
	if v, ok := got["allow_script"].(bool); !ok || v {
		t.Fatalf("get allow_script: got %v, want false", got["allow_script"])
	}

	code, patched = doModelRequest(t, s, http.MethodPatch, "/api/v1/sessions/1", map[string]any{
		"allow_script": true,
	})
	if code != http.StatusOK {
		t.Fatalf("patch on: code %d, resp %v", code, patched)
	}
	if v, ok := patched["allow_script"].(bool); !ok || !v {
		t.Fatalf("patch on allow_script: got %v, want true", patched["allow_script"])
	}
}

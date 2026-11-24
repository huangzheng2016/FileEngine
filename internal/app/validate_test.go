package app

import (
	"context"
	"testing"
)

// api.vd annotations in the IDL are only enforced when the binder validates;
// these requests are rejected by a vd rule alone.
func TestIDLValidationRulesAreEnforced(t *testing.T) {
	a := newTestApp(t)
	base := serve(t, a)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	json := map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + token()}

	for name, tc := range map[string]struct{ path, body string }{
		"empty login token":       {"/api/auth/login", `{"token":""}`},
		"copy or move":            {"/api/execute/start", `{"session_id":"1","mode":"x"}`},
		"empty chat content":      {"/api/chat/send", `{"session_id":"1","content":""}`},
		"no file ids to reset":    {"/api/plan/ops_reset", `{"session_id":"1","file_ids":[]}`},
		"negative plan version":   {"/api/plan/update", `{"session_id":"1","content":"x","version":-1}`},
		"category without a fs":   {"/api/category/create", `{"name":"a","path":"/a"}`},
		"filesystem without name": {"/api/filesystem/create", `{"protocol":"local"}`},
	} {
		st, body := do(t, testClient, "POST", base+tc.path, tc.body, json)
		if st != 400 || body["code"] != float64(1001) {
			t.Errorf("%s: %s = %d %v, want 400 invalid", name, tc.path, st, body)
		}
	}
	// a valid body passes validation and reaches the service
	if st, _ := do(t, testClient, "POST", base+"/api/execute/start", `{"session_id":"1","mode":"copy"}`, json); st != 404 {
		t.Errorf("valid execute start = %d, want 404 (no such session)", st)
	}
}

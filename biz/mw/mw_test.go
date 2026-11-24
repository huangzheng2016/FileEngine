package mw_test

import (
	"encoding/json"
	"strings"
	"testing"

	"FileEngine/biz/testenv"

	"github.com/cloudwego/hertz/pkg/common/ut"
)

func envelope(t *testing.T, w *ut.ResponseRecorder) (int, map[string]any) {
	t.Helper()
	res := w.Result()
	var out map[string]any
	_ = json.Unmarshal(res.Body(), &out)
	return res.StatusCode(), out
}

func TestAuthIsDefaultDeny(t *testing.T) {
	e := testenv.New(t)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/auth/me", ""},
		{"GET", "/api/session/list", ""},
		{"GET", "/api/file/list?session_id=1", ""},
		{"POST", "/api/session/create", `{"filesystem_id":"1"}`},
		{"POST", "/api/admin/backup", ""},
		{"GET", "/api/event/stream?session_id=1", ""},
	} {
		st, body := envelope(t, e.Request(tc.method, tc.path, tc.body))
		if st != 401 || body["code"] != float64(1002) {
			t.Errorf("%s %s without token = %d %v", tc.method, tc.path, st, body)
		}
	}
	if st, _ := envelope(t, e.Request("GET", "/api/auth/me", "", e.Bearer())); st != 200 {
		t.Errorf("me with token = %d", st)
	}
	for _, bad := range []string{"Bearer nope", "Bearer ", "Basic " + e.Token, e.Token} {
		if st, _ := envelope(t, e.Request("GET", "/api/auth/me", "", ut.Header{Key: "Authorization", Value: bad})); st != 401 {
			t.Errorf("Authorization %q = %d, want 401", bad, st)
		}
	}
	// a malformed Authorization header never falls back to a valid cookie
	st, _ := envelope(t, e.Request("GET", "/api/auth/me", "",
		ut.Header{Key: "Authorization", Value: "Basic x"}, ut.Header{Key: "Cookie", Value: "fe_session=" + e.Token}))
	if st != 401 {
		t.Errorf("bad Authorization with good cookie = %d, want 401", st)
	}
	if st, _ := envelope(t, e.Request("GET", "/api/auth/me", "", ut.Header{Key: "Cookie", Value: "fe_session=" + e.Token})); st != 200 {
		t.Errorf("cookie = %d", st)
	}
}

func TestEveryAPIRouteRequiresAuthExceptLoginAndBootstrap(t *testing.T) {
	e := testenv.New(t)
	public := map[string]bool{"/api/auth/login": true, "/api/auth/bootstrap": true}
	routes := e.Engine.Routes()
	n := 0
	for _, r := range routes {
		if !strings.HasPrefix(r.Path, "/api/") || public[r.Path] {
			continue
		}
		n++
		st, _ := envelope(t, e.Request(r.Method, r.Path, ""))
		if st != 401 {
			t.Errorf("%s %s without token = %d, want 401", r.Method, r.Path, st)
		}
	}
	if n != 68 {
		t.Errorf("checked %d protected routes, want 68", n)
	}
}

func TestHostGuardFetchMetadataAndBodyLimit(t *testing.T) {
	e := testenv.New(t)
	if st, body := envelope(t, e.Request("GET", "/healthz", "")); st != 200 || body["status"] != "ok" {
		t.Fatalf("healthz = %d %v", st, body)
	}
	foreign := ut.PerformRequest(e.Engine, "GET", "http://evil.example/healthz", nil)
	if st, body := envelope(t, foreign); st != 403 || body["code"] != float64(1003) {
		t.Errorf("foreign Host = %d %v", st, body)
	}
	cross := ut.Header{Key: "Sec-Fetch-Site", Value: "cross-site"}
	if st, _ := envelope(t, e.Request("GET", "/api/auth/me", "", cross, ut.Header{Key: "Cookie", Value: "fe_session=" + e.Token})); st != 403 {
		t.Errorf("cross-site cookie request = %d, want 403", st)
	}
	if st, _ := envelope(t, e.Request("GET", "/api/auth/me", "", cross, e.Bearer())); st != 200 {
		t.Errorf("cross-site bearer request = %d, want 200", st)
	}
	if st, _ := envelope(t, e.Request("GET", "/api/auth/me", "", ut.Header{Key: "Origin", Value: "http://evil.example"}, e.Bearer())); st != 200 {
		t.Errorf("bearer is exempt from the origin check, got %d", st)
	}
	if st, _ := envelope(t, e.Request("GET", "/api/auth/me", "", ut.Header{Key: "Origin", Value: "http://evil.example"},
		ut.Header{Key: "Cookie", Value: "fe_session=" + e.Token})); st != 403 {
		t.Errorf("foreign Origin with cookie = %d, want 403", st)
	}
	big := `{"token":"` + strings.Repeat("a", 2<<20) + `"}`
	if st, body := envelope(t, e.Request("POST", "/api/auth/login", big)); st != 413 || body["code"] != float64(1006) {
		t.Errorf("2MB body = %d %v", st, body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := testenv.New(t)
	api := e.Request("GET", "/api/auth/me", "", e.Bearer()).Result()
	if string(api.Header.Peek("X-Frame-Options")) != "DENY" || !strings.Contains(string(api.Header.Peek("Content-Security-Policy")), "default-src 'none'") {
		t.Errorf("api headers: %q", api.Header.Header())
	}
	spa := e.Request("GET", "/sessions/1", "").Result()
	if spa.StatusCode() != 200 || !strings.HasPrefix(string(spa.Header.ContentType()), "text/html") {
		t.Errorf("SPA fallback = %d %q", spa.StatusCode(), spa.Header.ContentType())
	}
	if csp := string(spa.Header.Peek("Content-Security-Policy")); csp != "frame-ancestors 'none'" {
		t.Errorf("SPA CSP = %q", csp)
	}
	if st, body := envelope(t, e.Request("GET", "/api/nope", "", e.Bearer())); st != 404 || body["code"] != float64(1004) {
		t.Errorf("unknown api route = %d %v", st, body)
	}
}

func TestLoginBootstrapCookieAndSharedBudget(t *testing.T) {
	e := testenv.New(t)
	login := func(token string) *ut.ResponseRecorder {
		return e.Request("POST", "/api/auth/login", `{"token":"`+token+`"}`)
	}
	if st, _ := envelope(t, login("wrong")); st != 401 {
		t.Fatalf("wrong token = %d", st)
	}
	ok := login(e.Token).Result()
	cookie := string(ok.Header.Peek("Set-Cookie"))
	if ok.StatusCode() != 200 || !strings.Contains(cookie, "fe_session="+e.Token) ||
		!strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Strict") {
		t.Fatalf("login = %d %q", ok.StatusCode(), cookie)
	}
	boot := e.Request("GET", "/api/auth/bootstrap?token="+e.Token, "").Result()
	if boot.StatusCode() != 303 || string(boot.Header.Peek("Location")) != "/" || !strings.Contains(string(boot.Header.Peek("Set-Cookie")), "fe_session=") {
		t.Fatalf("bootstrap = %d %q", boot.StatusCode(), boot.Header.Header())
	}
	if st, _ := envelope(t, e.Request("GET", "/api/auth/bootstrap?token=bad", "")); st != 401 {
		t.Fatalf("bootstrap with a bad token = %d", st)
	}
	logout := e.Request("POST", "/api/auth/logout", "{}", e.Bearer()).Result()
	if logout.StatusCode() != 200 || !strings.Contains(string(logout.Header.Peek("Set-Cookie")), "fe_session=;") && !strings.Contains(string(logout.Header.Peek("Set-Cookie")), "Max-Age=0") && !strings.Contains(string(logout.Header.Peek("Set-Cookie")), "Max-Age=-1") {
		t.Fatalf("logout = %d %q", logout.StatusCode(), logout.Header.Peek("Set-Cookie"))
	}

	// login and bootstrap guess the same token, so they draw on one budget of 10
	e2 := testenv.New(t)
	passed := 0
	for i := 0; i < 24; i++ {
		var w *ut.ResponseRecorder
		if i%2 == 0 {
			w = e2.Request("POST", "/api/auth/login", `{"token":"x"}`)
		} else {
			w = e2.Request("GET", "/api/auth/bootstrap?token=x", "")
		}
		st, body := envelope(t, w)
		if st == 429 {
			if passed != 10 || body["code"] != float64(1007) {
				t.Fatalf("429 after %d attempts %v, want exactly 10", passed, body)
			}
			return
		}
		passed++
	}
	t.Fatal("no 429 after 24 attempts")
}

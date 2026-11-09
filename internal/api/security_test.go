package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func newSecTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	if err := db.Init(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "t.db")}); err != nil {
		t.Fatal(err)
	}
	if opts.Token == "" {
		opts.Token = "test-token-0123456789"
	}
	return NewServer(db.NewRepository(db.GetDB()), nil, opts)
}

func do(s *Server, method, target string, body any, mutate func(*http.Request)) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:8080"
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	return w
}

func bearer(tok string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}

func TestAuthMiddleware(t *testing.T) {
	s := newSecTestServer(t, Options{})

	if w := do(s, "GET", "/api/v1/sessions", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no credentials = %d, want 401", w.Code)
	}
	if !strings.Contains(do(s, "GET", "/api/v1/sessions", nil, nil).Body.String(), `"error"`) {
		t.Fatal("401 not in error envelope")
	}
	if w := do(s, "GET", "/api/v1/sessions", nil, bearer("wrong")); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer = %d", w.Code)
	}
	if w := do(s, "GET", "/api/v1/sessions", nil, bearer(s.opts.Token)); w.Code != http.StatusOK {
		t.Fatalf("bearer = %d", w.Code)
	}
	if w := do(s, "GET", "/api/v1/auth/me", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("me unauthenticated = %d", w.Code)
	}

	if w := do(s, "POST", "/api/v1/auth/login", map[string]string{"token": "nope"}, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d", w.Code)
	}
	w := do(s, "POST", "/api/v1/auth/login", map[string]string{"token": s.opts.Token}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != SessionCookie || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie = %+v", cookies)
	}
	withCookie := func(r *http.Request) { r.AddCookie(cookies[0]) }
	if w := do(s, "GET", "/api/v1/auth/me", nil, withCookie); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"desktop":false`) {
		t.Fatalf("me with cookie = %d", w.Code)
	}

	if w := do(s, "GET", BootstrapPath+"?token=bad", nil, nil); w.Code != http.StatusForbidden {
		t.Fatalf("bad bootstrap = %d", w.Code)
	}
	w = do(s, "GET", strings.TrimPrefix(s.BootstrapURL(""), ""), nil, nil)
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
		t.Fatalf("bootstrap = %d", w.Code)
	}

	w = do(s, "GET", "/healthz", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("healthz = %d", w.Code)
	}
	w = do(s, "OPTIONS", "/api/v1/sessions", nil, func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") })
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header present")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") != "DENY" ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("security headers missing: %v", w.Header())
	}
}

func TestCrossSiteAndHostGuards(t *testing.T) {
	s := newSecTestServer(t, Options{BindHost: "127.0.0.1"})
	login := do(s, "POST", "/api/v1/auth/login", map[string]string{"token": s.opts.Token}, func(r *http.Request) { r.Host = "127.0.0.1:8080" })
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	base := func(r *http.Request) { r.Host = "127.0.0.1:8080"; r.AddCookie(cookie) }

	cases := map[string]func(*http.Request){
		"cross-site":   func(r *http.Request) { base(r); r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"same-site":    func(r *http.Request) { base(r); r.Header.Set("Sec-Fetch-Site", "same-site") },
		"other origin": func(r *http.Request) { base(r); r.Header.Set("Origin", "http://evil.example") },
		"null origin":  func(r *http.Request) { base(r); r.Header.Set("Origin", "null") },
		"rebound host": func(r *http.Request) { base(r); r.Host = "evil.example:8080" },
	}
	for name, m := range cases {
		if w := do(s, "POST", "/api/v1/sessions", map[string]any{}, m); w.Code != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", name, w.Code)
		}
	}
	ok := func(r *http.Request) {
		base(r)
		r.Header.Set("Origin", "http://127.0.0.1:8080")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if w := do(s, "GET", "/api/v1/sessions", nil, ok); w.Code != http.StatusOK {
		t.Fatalf("same-origin = %d", w.Code)
	}
	if w := do(s, "GET", "/api/v1/sessions", nil, func(r *http.Request) { ok(r); r.Host = "localhost:8080" }); w.Code == http.StatusForbidden {
		if !strings.Contains(w.Body.String(), "跨站") {
			t.Fatalf("localhost host rejected by host guard: %s", w.Body)
		}
	}

	pinned := newSecTestServer(t, Options{AllowedHosts: []string{"127.0.0.1:43210"}})
	if w := do(pinned, "GET", "/api/v1/sessions", nil, func(r *http.Request) { r.Host = "localhost:43210"; bearer(pinned.opts.Token)(r) }); w.Code != http.StatusForbidden {
		t.Fatalf("pinned host mismatch = %d", w.Code)
	}
	if w := do(pinned, "GET", "/api/v1/sessions", nil, func(r *http.Request) { r.Host = "127.0.0.1:43210"; bearer(pinned.opts.Token)(r) }); w.Code != http.StatusOK {
		t.Fatalf("pinned host = %d", w.Code)
	}
	if w := do(pinned, "GET", "/api/v1/auth/me", nil, func(r *http.Request) { r.Host = "127.0.0.1:43210"; bearer(pinned.opts.Token)(r) }); !strings.Contains(w.Body.String(), `"desktop":true`) {
		t.Fatalf("desktop me = %s", w.Body)
	}
}

func TestBodyLimit(t *testing.T) {
	s := newSecTestServer(t, Options{})
	big := map[string]string{"name": strings.Repeat("x", 2<<20)}
	if w := do(s, "POST", "/api/v1/models", big, bearer(s.opts.Token)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d", w.Code)
	}
}

func TestSecretReuseRules(t *testing.T) {
	s := newSecTestServer(t, Options{})
	auth := bearer(s.opts.Token)

	w := do(s, "POST", "/api/v1/models", map[string]any{"name": "m", "provider": "openai", "model": "x", "base_url": "http://a.invalid/v1", "api_key": "sk-stored"}, auth)
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), "sk-stored") {
		t.Fatalf("create model = %d %s", w.Code, w.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created["api_key_set"] != true {
		t.Fatalf("api_key_set = %v", created["api_key_set"])
	}
	id := uint(created["id"].(float64))
	w = do(s, "PUT", "/api/v1/models/"+itoa(id), map[string]any{"name": "m2", "provider": "openai", "model": "x", "base_url": "http://a.invalid/v1"}, auth)
	if w.Code != http.StatusOK {
		t.Fatalf("update keep = %d %s", w.Code, w.Body)
	}
	m, _ := s.repo.GetModelProvider(id)
	if m.APIKey != "sk-stored" {
		t.Fatalf("omitted api_key changed the stored key: %q", m.APIKey)
	}
	w = do(s, "PUT", "/api/v1/models/"+itoa(id), map[string]any{"name": "m2", "provider": "openai", "model": "x", "base_url": "http://evil.invalid/v1"}, auth)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("redirect stored key on update = %d", w.Code)
	}
	w = do(s, "POST", "/api/v1/models/test", map[string]any{"id": id, "name": "m2", "provider": "openai", "model": "x", "base_url": "http://evil.invalid/v1"}, auth)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("test endpoint reused key for a different base_url: %d %s", w.Code, w.Body)
	}

	dir := t.TempDir()
	w = do(s, "POST", "/api/v1/filesystems", map[string]any{"name": "f", "protocol": "local", "base_path": dir, "host": "h", "username": "u", "password": "pw-stored"}, auth)
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), "pw-stored") || !strings.Contains(w.Body.String(), `"password_set":true`) {
		t.Fatalf("create fs = %d %s", w.Code, w.Body)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	fsID := uint(created["id"].(float64))
	w = do(s, "POST", "/api/v1/filesystems/test", map[string]any{"id": fsID, "name": "f", "protocol": "local", "base_path": dir, "host": "attacker.invalid", "username": "u"}, auth)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("test endpoint reused password for another host: %d %s", w.Code, w.Body)
	}
	w = do(s, "POST", "/api/v1/filesystems/test", map[string]any{"id": fsID, "name": "f", "protocol": "local", "base_path": dir, "host": "h", "username": "u"}, auth)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("same-endpoint test = %d %s", w.Code, w.Body)
	}
	w = do(s, "PUT", "/api/v1/filesystems/"+itoa(fsID), map[string]any{"name": "f", "protocol": "local", "base_path": dir, "host": "attacker.invalid", "username": "u"}, auth)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("endpoint change keeping password = %d", w.Code)
	}

	var raw string
	s.repo.DB().Raw("SELECT password FROM filesystems WHERE id = ?", fsID).Scan(&raw)
	if raw == "" || strings.Contains(raw, "pw-stored") {
		t.Fatalf("password stored in plaintext: %q", raw)
	}
	s.repo.DB().Raw("SELECT api_key FROM model_providers WHERE id = ?", id).Scan(&raw)
	if strings.Contains(raw, "sk-stored") {
		t.Fatalf("api key stored in plaintext: %q", raw)
	}

	w = do(s, "POST", "/api/v1/filesystems", map[string]any{"name": "etc", "protocol": "local", "base_path": "/etc"}, auth)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("local /etc = %d", w.Code)
	}
}

func TestConfigRedactionAndMerge(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "server:\n  token: tok-abcdefghijklmnop\ndatabase:\n  driver: postgres\n  dsn: host=x password=pgsecret\nagent:\n  concurrency: 3\n  max_step: 55\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p); err != nil {
		t.Fatal(err)
	}
	s := newSecTestServer(t, Options{})
	auth := bearer(s.opts.Token)

	w := do(s, "GET", "/api/v1/config", nil, auth)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "pgsecret") || strings.Contains(w.Body.String(), "tok-abc") || strings.Contains(w.Body.String(), "dsn") {
		t.Fatalf("config leak: %s", w.Body)
	}
	w = do(s, "PUT", "/api/v1/config", map[string]any{
		"server": map[string]any{"host": "0.0.0.0"},
		"agent":  map[string]any{"concurrency": 7},
	}, auth)
	if w.Code != http.StatusOK {
		t.Fatalf("put config = %d %s", w.Code, w.Body)
	}
	cfg := config.Get()
	if cfg.Agent.Concurrency != 7 || cfg.Agent.MaxStep != 55 || cfg.Server.Host != "127.0.0.1" {
		t.Fatalf("merge wrong: %+v / host %s", cfg.Agent.Concurrency, cfg.Server.Host)
	}
	saved, _ := os.ReadFile(p)
	if !strings.Contains(string(saved), "concurrency: 7") || !strings.Contains(string(saved), "tok-abcdefghijklmnop") {
		t.Fatalf("not saved to the loaded path: %s", saved)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v", st.Mode().Perm())
	}
}

func TestFilePatchRejectsEscapingPath(t *testing.T) {
	s := newSecTestServer(t, Options{})
	sess := &db.ScanSession{Status: "scanned"}
	_ = s.repo.CreateSession(sess)
	f := &db.FileEntry{ScanSessionID: sess.ID, OriginalPath: "a.txt", Name: "a.txt", FileType: "file"}
	_ = s.repo.CreateFile(f)
	for _, bad := range []string{"../../etc/x", "/a/../../b", "/"} {
		w := do(s, "PATCH", "/api/v1/files/"+itoa(f.ID), map[string]any{"new_path": bad}, bearer(s.opts.Token))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("PATCH new_path %q = %d", bad, w.Code)
		}
	}
	got, _ := s.repo.GetFile(f.ID)
	if got.NewPath != "" {
		t.Fatalf("escaping target written: %q", got.NewPath)
	}
}

func TestContentDisposition(t *testing.T) {
	v := contentDisposition("inline", "evil\"\r\nSet-Cookie: x=y.txt")
	if strings.ContainsAny(v, "\r\n") {
		t.Fatalf("header injection: %q", v)
	}
	if v := contentDisposition("attachment", "报告.pdf"); !strings.HasPrefix(v, "attachment; filename*=") {
		t.Fatalf("non-ASCII name: %q", v)
	}
}

func TestRateLimiter(t *testing.T) {
	l := newRateLimiter(2, 60e9)
	if !l.allow("k") || !l.allow("k") || l.allow("k") {
		t.Fatal("limit not enforced")
	}
	if !l.allow("other") {
		t.Fatal("keys not independent")
	}
}

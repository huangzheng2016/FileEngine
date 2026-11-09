package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"FileEngine/internal/db"

	"golang.org/x/crypto/ssh"
)

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
	return m
}

func TestFilesystemPrivateKeyStoredEncrypted(t *testing.T) {
	s := newSecTestServer(t, Options{})
	auth := bearer(s.opts.Token)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, _ := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("pp-secret"))
	key := string(pem.EncodeToMemory(block))
	body := map[string]any{"name": "nas", "protocol": "sftp", "base_path": "/data", "host": "192.0.2.1", "port": 22,
		"username": "u", "private_key": key, "key_passphrase": "pp-secret", "host_key": "SHA256:pinned"}

	w := do(s, "POST", "/api/v1/filesystems", body, auth)
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), "PRIVATE KEY") || strings.Contains(w.Body.String(), "pp-secret") {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	created := decode(t, w)
	if created["private_key_set"] != true || created["key_passphrase_set"] != true || created["password_set"] != false {
		t.Fatalf("flags = %v", created)
	}
	id := uint(created["id"].(float64))
	var raw struct{ PrivateKey, KeyPassphrase string }
	s.repo.DB().Raw("SELECT private_key, key_passphrase FROM filesystems WHERE id = ?", id).Scan(&raw)
	if raw.PrivateKey == "" || strings.Contains(raw.PrivateKey, "PRIVATE KEY") || strings.Contains(raw.KeyPassphrase, "pp-secret") {
		t.Fatalf("key stored in plaintext: %+v", raw)
	}

	// Moving to another host keeps the key: public-key auth never reveals it.
	delete(body, "private_key")
	delete(body, "key_passphrase")
	body["host"] = "192.0.2.2"
	if w := do(s, "PUT", "/api/v1/filesystems/"+itoa(id), body, auth); w.Code != http.StatusOK {
		t.Fatalf("update keeping key = %d %s", w.Code, w.Body)
	}
	fs, _ := s.repo.GetFilesystem(id)
	if fs.PrivateKey != key || fs.KeyPassphrase != "pp-secret" {
		t.Fatal("omitted key fields changed the stored key")
	}

	// Switching to password auth clears the key.
	body["private_key"], body["key_passphrase"], body["password"] = "", "", "pw"
	if w := do(s, "PUT", "/api/v1/filesystems/"+itoa(id), body, auth); w.Code != http.StatusOK {
		t.Fatalf("switch to password = %d %s", w.Code, w.Body)
	}
	fs, _ = s.repo.GetFilesystem(id)
	if fs.PrivateKey != "" || fs.KeyPassphrase != "" || fs.Password != "pw" {
		t.Fatalf("after switch: %+v", fs)
	}

	body["private_key"], body["password"] = "not a key", ""
	w = do(s, "PUT", "/api/v1/filesystems/"+itoa(id), body, auth)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "私钥格式不对") {
		t.Fatalf("invalid key = %d %s", w.Code, w.Body)
	}
}

func TestFilesystemProbeListsFolders(t *testing.T) {
	s := newSecTestServer(t, Options{})
	auth := bearer(s.opts.Token)
	dir := t.TempDir()
	for _, d := range []string{"Photos", "docs", ".hidden"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := do(s, "POST", "/api/v1/filesystems/test", map[string]any{"protocol": "local", "base_path": ""}, auth)
	roots := decode(t, w)
	if roots["success"] != true || len(roots["dirs"].([]any)) == 0 {
		t.Fatalf("roots = %s", w.Body)
	}

	w = do(s, "POST", "/api/v1/filesystems/test", map[string]any{"protocol": "local", "base_path": dir}, auth)
	res := decode(t, w)
	dirs := res["dirs"].([]any)
	if res["success"] != true || len(dirs) != 2 {
		t.Fatalf("listing = %s", w.Body)
	}
	first := dirs[0].(map[string]any)
	if first["name"] != "docs" || first["path"] != filepath.Join(dir, "docs") {
		t.Fatalf("first dir = %v", first)
	}

	w = do(s, "POST", "/api/v1/filesystems/test", map[string]any{"protocol": "local", "base_path": "/etc"}, auth)
	res = decode(t, w)
	if w.Code != http.StatusOK || res["success"] != false || !strings.Contains(res["error"].(string), "FE_LOCAL_ROOTS") {
		t.Fatalf("outside roots = %d %s", w.Code, w.Body)
	}

	w = do(s, "POST", "/api/v1/filesystems/test", map[string]any{"protocol": "sftp", "host": "127.0.0.1", "port": 1}, auth)
	res = decode(t, w)
	if res["success"] != false || !strings.Contains(res["error"].(string), "拒绝连接") || res["detail"] == nil {
		t.Fatalf("refused = %s", w.Body)
	}
}

func TestModelCatalog(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key"}}`))
			return
		}
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"zeta-chat"},{"id":"alpha-vision"},{"id":""},{"id":"alpha-vision"}]}`))
	}))
	defer upstream.Close()
	s := newSecTestServer(t, Options{})
	auth := bearer(s.opts.Token)
	base := upstream.URL + "/v1"

	w := do(s, "POST", "/api/v1/models/catalog", map[string]any{"provider": "openai", "base_url": base, "api_key": "sk-good"}, auth)
	res := decode(t, w)
	if res["success"] != true || len(res["models"].([]any)) != 2 || res["models"].([]any)[0] != "alpha-vision" {
		t.Fatalf("catalog = %s", w.Body)
	}

	w = do(s, "POST", "/api/v1/models/catalog", map[string]any{"provider": "openai", "base_url": base, "api_key": "sk-bad"}, auth)
	res = decode(t, w)
	if res["success"] != false || !strings.Contains(res["error"].(string), "密钥不对") {
		t.Fatalf("bad key = %s", w.Body)
	}

	m := &db.ModelProvider{Name: "m", Provider: "openai", Model: "x", BaseURL: base, APIKey: "sk-good"}
	if err := s.repo.CreateModelProvider(m); err != nil {
		t.Fatal(err)
	}
	w = do(s, "POST", "/api/v1/models/catalog", map[string]any{"id": m.ID, "provider": "openai", "base_url": base}, auth)
	if res = decode(t, w); res["success"] != true {
		t.Fatalf("stored key reuse = %s", w.Body)
	}
	w = do(s, "POST", "/api/v1/models/catalog", map[string]any{"id": m.ID, "provider": "openai", "base_url": "http://attacker.invalid/v1"}, auth)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("stored key sent to another base_url: %d %s", w.Code, w.Body)
	}
}

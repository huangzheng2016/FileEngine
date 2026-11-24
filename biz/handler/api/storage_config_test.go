package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"FileEngine/biz/testenv"
	"FileEngine/internal/config"

	"github.com/cloudwego/hertz/pkg/common/ut"
)

func callRaw(t *testing.T, e *testenv.Env, method, path, body string) (int, map[string]any, []byte) {
	t.Helper()
	res := e.Request(method, path, body, e.Bearer()).Result()
	var out map[string]any
	_ = json.Unmarshal(res.Body(), &out)
	return res.StatusCode(), out, res.Body()
}

func TestConfigVersionedSaveAndConflict(t *testing.T) {
	e := testenv.New(t)
	st, got, _ := callRaw(t, e, "GET", "/api/config/get", "")
	if st != 200 || got["version"] != float64(0) {
		t.Fatalf("get: %d %v", st, got)
	}
	st, got, _ = callRaw(t, e, "POST", "/api/config/update", `{"agent":{"concurrency":3},"base_version":0}`)
	if st != 200 || got["version"] != float64(1) || config.Get().Agent.Concurrency != 3 {
		t.Fatalf("update: %d %v", st, got)
	}
	st, got, _ = callRaw(t, e, "POST", "/api/config/update", `{"agent":{"concurrency":4},"base_version":0}`)
	if st != 409 || !strings.Contains(got["msg"].(string), "已被其他人修改") || config.Get().Agent.Concurrency != 3 {
		t.Fatalf("stale: %d %v", st, got)
	}
	st, got, _ = callRaw(t, e, "POST", "/api/config/update", `{"agent":{"concurrency":0},"base_version":1}`)
	if st != 400 || !strings.Contains(got["msg"].(string), "并发数") {
		t.Fatalf("invalid: %d %v", st, got)
	}
	st, got, _ = callRaw(t, e, "POST", "/api/config/prompt_update", `{"prompt":"自定义","base_version":1}`)
	if st != 200 || got["version"] != float64(2) || got["is_custom"] != true {
		t.Fatalf("prompt: %d %v", st, got)
	}
	st, got, _ = callRaw(t, e, "GET", "/api/config/versions", "")
	items, _ := got["items"].([]any)
	if st != 200 || len(items) != 2 || items[0].(map[string]any)["version"] != float64(2) {
		t.Fatalf("versions: %d %v", st, got)
	}
}

func TestFilesystemErrorsAreChinese(t *testing.T) {
	e := testenv.New(t)
	st, got, _ := callRaw(t, e, "GET", "/api/filesystem/get?id=42", "")
	if st != 404 || got["msg"] != "存储不存在" {
		t.Fatalf("missing: %d %v", st, got)
	}
	st, got, _ = callRaw(t, e, "POST", "/api/filesystem/create", `{"name":"a","protocol":"webdav","base_path":"/x"}`)
	if st != 400 || !strings.Contains(got["msg"].(string), "不支持的协议") {
		t.Fatalf("protocol: %d %v", st, got)
	}
	st, got, _ = callRaw(t, e, "POST", "/api/filesystem/create", `{"name":"nas","protocol":"ftp","host":"192.168.1.10","port":21,"base_path":"/d","password":"pw"}`)
	if st != 200 {
		t.Fatalf("create: %d %v", st, got)
	}
	item := got["item"].(map[string]any)
	if item["password_set"] != true || item["id"] == "" {
		t.Fatalf("item: %v", item)
	}
	if strings.Contains(string(e.Request("GET", "/api/filesystem/list", "", e.Bearer()).Result().Body()), `"pw"`) {
		t.Fatal("secret leaked")
	}
	st, got, _ = callRaw(t, e, "POST", "/api/filesystem/delete", `{"id":"`+item["id"].(string)+`"}`)
	if st != 200 {
		t.Fatalf("delete: %d %v", st, got)
	}
}

func TestModelErrorsAreChinese(t *testing.T) {
	e := testenv.New(t)
	st, got, _ := callRaw(t, e, "POST", "/api/model/create", `{"name":"m","provider":"openai","model":"x","api_key":"sk"}`)
	if st != 200 {
		t.Fatalf("create: %d %v", st, got)
	}
	item := got["item"].(map[string]any)
	if item["api_key_set"] != true || item["context_window"] != float64(200000) {
		t.Fatalf("item: %v", item)
	}
	st, got, _ = callRaw(t, e, "POST", "/api/model/test", `{"provider":"nope"}`)
	if st != 400 || got["msg"] != "不支持的模型提供方" {
		t.Fatalf("test: %d %v", st, got)
	}
	st, got, _ = callRaw(t, e, "POST", "/api/model/delete", `{"id":"0"}`)
	if st != 400 {
		t.Fatalf("bad id: %d %v", st, got)
	}
}

func TestBackupDownloadsSnapshot(t *testing.T) {
	e := testenv.New(t)
	res := e.Request("POST", "/api/admin/backup", "", e.Bearer()).Result()
	if res.StatusCode() != 200 || !strings.HasPrefix(string(res.Body()), "SQLite format 3") {
		t.Fatalf("backup: %d %q", res.StatusCode(), res.Header.Get("Content-Disposition"))
	}
	if !strings.Contains(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("disposition = %q", res.Header.Get("Content-Disposition"))
	}
}

func TestBootstrapIsRateLimited(t *testing.T) {
	e := testenv.New(t)
	last := 0
	for range 12 {
		last = e.Request("GET", "/api/auth/bootstrap?token=wrong", "", ut.Header{Key: "X-Test", Value: "1"}).Result().StatusCode()
	}
	if last != 429 {
		t.Fatalf("12th bootstrap guess = %d, want 429", last)
	}
}

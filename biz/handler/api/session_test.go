package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/testenv"
	"FileEngine/container"
	"FileEngine/internal/remotefs"
)

type fixture struct {
	env     *testenv.Env
	root    string
	fsID    uint
	modelID uint
}

// newFixture starts the backend with one local storage (a temp dir holding a.txt
// and sub/b.pdf) and one model provider.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv(remotefs.EnvLocalRoots, root)
	env := testenv.New(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.pdf"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := container.Default.DB
	fs := &model.Filesystem{Name: "本地", Protocol: "local", BasePath: root}
	m := &model.ModelProvider{Name: "m", Provider: "openai", Model: "x"}
	if err := g.Create(fs).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.Create(m).Error; err != nil {
		t.Fatal(err)
	}
	return &fixture{env: env, root: root, fsID: fs.ID, modelID: m.ID}
}

func (f *fixture) call(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	w := f.env.Request(method, path, body, f.env.Bearer())
	res := w.Result()
	out := map[string]any{}
	if b := res.Body(); len(b) > 0 {
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("%s %s: bad json %q", method, path, b)
		}
	}
	return res.StatusCode(), out
}

func (f *fixture) createSession(t *testing.T, extra string) map[string]any {
	t.Helper()
	body := fmt.Sprintf(`{"filesystem_id":"%d","model_provider_id":"%d"%s}`, f.fsID, f.modelID, extra)
	code, out := f.call(t, http.MethodPost, "/api/session/create", body)
	if code != http.StatusOK {
		t.Fatalf("create session: %d %v", code, out)
	}
	return out["item"].(map[string]any)
}

func (f *fixture) waitScanned(t *testing.T, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, out := f.call(t, http.MethodGet, "/api/session/get?id="+id, "")
		item := out["item"].(map[string]any)
		if item["status"] != "scanning" {
			return item
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("scan did not finish")
	return nil
}

func TestCreateSessionWithTemplatesAndSwitches(t *testing.T) {
	f := newFixture(t)
	item := f.createSession(t, `,"allow_script":false,"allow_read_file":false,"allow_auto_category":false,"extract_metadata":false,
		"scope_exts":".PDF, txt","goal":"  整理照片 ",
		"rules":[{"name":"pdf","exts":"pdf","target":"/文档/{name}"},{"name":"txt","exts":"txt","target":"/文本/{name}"}],
		"categories":[{"name":"文档","path":"/文档"},{"name":"文本","path":"/文本","description":"d"}]`)
	id := item["id"].(string)
	done := f.waitScanned(t, id)
	if done["status"] != "scanned" {
		t.Fatalf("scan ended as %v", done)
	}
	for _, k := range []string{"allow_script", "allow_read_file", "allow_auto_category", "extract_metadata"} {
		if done[k] != false {
			t.Errorf("%s = %v, want false", k, done[k])
		}
	}
	if done["goal"] != "整理照片" || done["scope_exts"] != "pdf,txt" {
		t.Errorf("goal/scope = %v / %v", done["goal"], done["scope_exts"])
	}
	if done["error_message"] != "" || done["scan_warning"] != "" {
		t.Errorf("a clean scan left messages: %v", done)
	}

	var rules []model.PlanRule
	if err := container.Default.DB.Order("position").Find(&rules).Error; err != nil || len(rules) != 2 || rules[0].Position != 1 || rules[1].Position != 2 || rules[0].Source != "user" {
		t.Fatalf("rules = %+v (%v)", rules, err)
	}
	var cats []model.Category
	if err := container.Default.DB.Find(&cats).Error; err != nil || len(cats) != 2 {
		t.Fatalf("categories = %+v (%v)", cats, err)
	}
}

func TestCreateSessionRejectsABadTemplateWithoutCreatingAnything(t *testing.T) {
	f := newFixture(t)
	body := fmt.Sprintf(`{"filesystem_id":"%d","model_provider_id":"%d","rules":[{"name":"x","target":"{nope}"}]}`, f.fsID, f.modelID)
	code, out := f.call(t, http.MethodPost, "/api/session/create", body)
	if code != http.StatusBadRequest || !strings.Contains(out["msg"].(string), "未知变量") {
		t.Fatalf("bad rule: %d %v", code, out)
	}
	var n int64
	container.Default.DB.Model(&model.ScanSession{}).Count(&n)
	if n != 0 {
		t.Fatalf("%d sessions created by a rejected request", n)
	}
}

func TestTaggingWhileScanningIsAConflict(t *testing.T) {
	f := newFixture(t)
	s := &model.ScanSession{FilesystemID: f.fsID, ModelProviderID: f.modelID, Status: "scanning"}
	if err := container.Default.DB.Create(s).Error; err != nil {
		t.Fatal(err)
	}
	code, out := f.call(t, http.MethodPost, "/api/run/tag", fmt.Sprintf(`{"session_id":"%d"}`, s.ID))
	if code != http.StatusConflict || out["msg"] != "扫描还没结束" {
		t.Fatalf("tag during scan: %d %v", code, out)
	}
}

func TestUpdateDuringScanKeepsTheEdit(t *testing.T) {
	f := newFixture(t)
	item := f.createSession(t, `,"goal":"旧"`)
	id := item["id"].(string)
	code, out := f.call(t, http.MethodPost, "/api/session/update", fmt.Sprintf(`{"id":"%s","goal":"新目标"}`, id))
	if code != http.StatusOK {
		t.Fatalf("update: %d %v", code, out)
	}
	done := f.waitScanned(t, id)
	if done["goal"] != "新目标" {
		t.Fatalf("goal after the scan = %v", done["goal"])
	}
}

func TestDeleteSessionRemovesEverythingOfIt(t *testing.T) {
	f := newFixture(t)
	item := f.createSession(t, `,"rules":[{"name":"r","target":"/x/{name}"}]`)
	id := item["id"].(string)
	f.waitScanned(t, id)
	var sid uint
	fmt.Sscan(id, &sid)
	g := container.Default.DB
	for _, row := range []any{
		&model.ExecOp{SessionID: sid}, &model.SessionPlan{SessionID: sid}, &model.ChatTurn{SessionID: sid},
		&model.MemoryItem{Scope: "session", ScopeID: sid, Key: "k"},
		&model.SessionEvent{SessionID: sid, Type: "x"},
	} {
		if err := g.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	code, out := f.call(t, http.MethodPost, "/api/session/delete", fmt.Sprintf(`{"id":"%s"}`, id))
	if code != http.StatusOK {
		t.Fatalf("delete: %d %v", code, out)
	}
	for name, table := range map[string]any{
		"file_entries": &model.FileEntry{}, "plan_rules": &model.PlanRule{}, "exec_ops": &model.ExecOp{},
		"plans": &model.SessionPlan{}, "chat": &model.ChatTurn{}, "memory": &model.MemoryItem{},
		"events": &model.SessionEvent{}, "runs": &model.AgentRun{}, "sessions": &model.ScanSession{},
	} {
		var n int64
		if err := g.Model(table).Count(&n).Error; err != nil || n != 0 {
			t.Errorf("%s: %d rows left (%v)", name, n, err)
		}
	}
	// a second delete of the gone session succeeds
	if code, _ := f.call(t, http.MethodPost, "/api/session/delete", fmt.Sprintf(`{"id":"%s"}`, id)); code != http.StatusOK {
		t.Fatalf("repeat delete: %d", code)
	}
}

func TestAutoTagStartsTaggingAfterTheScanCompletes(t *testing.T) {
	f := newFixture(t)
	item := f.createSession(t, `,"auto_tag":true`)
	id := item["id"].(string)
	// while the scan runs, an early request is told that tagging follows by itself
	f.waitScanned(t, id)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, out := f.call(t, http.MethodGet, "/api/run/list?session_id="+id, "")
		if items, _ := out["items"].([]any); len(items) > 0 {
			if items[len(items)-1].(map[string]any)["phase"] != "tagging" {
				t.Fatalf("first run = %v", items[len(items)-1])
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no tagging run started after the scan")
}

func TestStatsAndRunListOfAFreshSession(t *testing.T) {
	f := newFixture(t)
	item := f.createSession(t, "")
	id := item["id"].(string)
	f.waitScanned(t, id)
	code, out := f.call(t, http.MethodGet, "/api/session/stats?id="+id, "")
	if code != http.StatusOK || out["total_files"].(float64) != 3 {
		t.Fatalf("stats: %d %v", code, out)
	}
	code, out = f.call(t, http.MethodGet, "/api/run/list?session_id="+id, "")
	if code != http.StatusOK || out["total"].(float64) != 0 {
		t.Fatalf("runs: %d %v", code, out)
	}
	code, out = f.call(t, http.MethodGet, "/api/session/get?id=999", "")
	if code != http.StatusNotFound {
		t.Fatalf("missing session: %d %v", code, out)
	}
}

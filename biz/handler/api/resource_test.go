package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"FileEngine/biz/dal/db/model"
	"FileEngine/biz/testenv"
	"FileEngine/container"
)

type fixture struct {
	e       *testenv.Env
	fsID    uint
	session uint
}

func call(t *testing.T, e *testenv.Env, method, path, body string) (int, map[string]any) {
	t.Helper()
	w := e.Request(method, path, body, e.Bearer())
	res := w.Result()
	var out map[string]any
	if raw := res.Body(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: bad json %q", method, path, raw)
		}
	}
	return res.StatusCode(), out
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	e := testenv.New(t)
	fs := &model.Filesystem{Name: "fs-" + t.Name(), Protocol: "local", BasePath: t.TempDir()}
	if err := container.Default.DB.Create(fs).Error; err != nil {
		t.Fatal(err)
	}
	return &fixture{e: e, fsID: fs.ID, session: newSession(t, fs.ID)}
}

func newSession(t *testing.T, fsID uint) uint {
	t.Helper()
	s := &model.ScanSession{FilesystemID: fsID, Status: "scanned", Protocol: "local"}
	if err := container.Default.DB.Create(s).Error; err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func addFiles(t *testing.T, session uint, files ...model.FileEntry) {
	t.Helper()
	for i := range files {
		files[i].ScanSessionID = session
	}
	if err := container.Default.Repo.CreateFiles(files); err != nil {
		t.Fatal(err)
	}
}

func TestFileListRootAndTree(t *testing.T) {
	f := newFixture(t)
	addFiles(t, f.session,
		model.FileEntry{OriginalPath: "a", Name: "a", FileType: "directory", ParentPath: "", SubdirCount: 1, ChildCount: 2},
		model.FileEntry{OriginalPath: "a/sub", Name: "sub", FileType: "directory", ParentPath: "a", Depth: 1},
		model.FileEntry{OriginalPath: "a/b.txt", Name: "b.txt", FileType: "file", ParentPath: "a", Depth: 1},
		model.FileEntry{OriginalPath: "c.txt", Name: "c.txt", FileType: "file", ParentPath: ""},
	)
	_, body := call(t, f.e, "GET", fmt.Sprintf("/api/file/list?session_id=%d&parent_path=", f.session), "")
	if body["total"] != float64(2) {
		t.Fatalf("root listing must hold only the scan root's children, got %v", body)
	}
	if body["review_confidence"] != 0.6 {
		t.Fatalf("review_confidence = %v", body["review_confidence"])
	}
	_, body = call(t, f.e, "GET", fmt.Sprintf("/api/file/list?session_id=%d", f.session), "")
	if body["total"] != float64(4) {
		t.Fatalf("no parent_path means no folder filter, got %v", body["total"])
	}
	_, body = call(t, f.e, "GET", fmt.Sprintf("/api/file/list?session_id=%d&parent_path=a&page_size=1000", f.session), "")
	if body["total"] != float64(2) {
		t.Fatalf("parent a: %v", body["total"])
	}
	_, body = call(t, f.e, "GET", fmt.Sprintf("/api/file/tree?session_id=%d&parent_path=", f.session), "")
	nodes := body["items"].([]any)
	if len(nodes) != 1 || nodes[0].(map[string]any)["is_leaf"] != false {
		t.Fatalf("tree: %v", body)
	}
	if status, _ := call(t, f.e, "GET", "/api/file/list?session_id=9999", ""); status != 404 {
		t.Fatalf("unknown session: %d", status)
	}
}

func TestFileUpdateRecordsCorrection(t *testing.T) {
	f := newFixture(t)
	addFiles(t, f.session, model.FileEntry{OriginalPath: "dl/x.jpg", Name: "x.jpg", FileType: "file", ParentPath: "dl", Depth: 1})
	var entry model.FileEntry
	container.Default.DB.Where("original_path = ?", "dl/x.jpg").Take(&entry)

	status, body := call(t, f.e, "POST", "/api/file/update", fmt.Sprintf(`{"id":"%d","new_path":"/图片/x.jpg"}`, entry.ID))
	if status != 200 || body["item"].(map[string]any)["new_path"] != "/图片/x.jpg" {
		t.Fatalf("update: %d %v", status, body)
	}
	_, mem := call(t, f.e, "GET", fmt.Sprintf("/api/memory/list?scope=filesystem&scope_id=%d", f.fsID), "")
	items := mem["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["kind"] != "correction" {
		t.Fatalf("a user edit must record a correction memory: %v", mem)
	}
	if status, _ := call(t, f.e, "POST", "/api/file/update", fmt.Sprintf(`{"id":"%d","new_path":"../x"}`, entry.ID)); status != 400 {
		t.Fatalf("invalid target: %d", status)
	}
}

func TestCategoryConflictsAndScopedCascade(t *testing.T) {
	f := newFixture(t)
	other := &model.Filesystem{Name: "other", Protocol: "local", BasePath: t.TempDir()}
	container.Default.DB.Create(other)
	otherSession := newSession(t, other.ID)

	create := func(fs uint, name, path string) (int, map[string]any) {
		return call(t, f.e, "POST", "/api/category/create", fmt.Sprintf(`{"filesystem_id":"%d","name":%q,"path":%q}`, fs, name, path))
	}
	status, body := create(f.fsID, "照片", "/photo")
	if status != 200 {
		t.Fatalf("create: %d %v", status, body)
	}
	catID := body["item"].(map[string]any)["id"].(string)
	if status, body := create(f.fsID, "照片", "/other"); status != 409 || body["msg"] == "" {
		t.Fatalf("duplicate name: %d %v", status, body)
	}
	if status, _ := create(f.fsID, "另一个", "/photo"); status != 409 {
		t.Fatalf("duplicate path: %d", status)
	}
	if status, _ := create(999, "x", "/x"); status != 404 {
		t.Fatalf("unknown filesystem: %d", status)
	}

	addFiles(t, f.session, model.FileEntry{OriginalPath: "a.jpg", Name: "a.jpg", FileType: "file", NewPath: "/photo/a.jpg", NewPathKey: "photo/a.jpg", Operation: "planned", Role: "file"})
	addFiles(t, otherSession, model.FileEntry{OriginalPath: "b.jpg", Name: "b.jpg", FileType: "file", NewPath: "/photo/b.jpg", NewPathKey: "photo/b.jpg", Operation: "planned", Role: "file"})

	status, body = call(t, f.e, "POST", "/api/category/update", fmt.Sprintf(`{"id":"%s","filesystem_id":"%d","name":"照片","path":"/pics"}`, catID, other.ID))
	if status != 400 {
		t.Fatalf("moving a category to another filesystem must be refused: %d %v", status, body)
	}
	status, body = call(t, f.e, "POST", "/api/category/update", fmt.Sprintf(`{"id":"%s","filesystem_id":"%d","name":"照片","path":"/pics"}`, catID, f.fsID))
	if status != 200 || body["item"].(map[string]any)["filesystem_id"] != fmt.Sprint(f.fsID) {
		t.Fatalf("update: %d %v", status, body)
	}
	var a, b model.FileEntry
	container.Default.DB.Where("original_path = ?", "a.jpg").Take(&a)
	container.Default.DB.Where("original_path = ?", "b.jpg").Take(&b)
	if a.NewPath != "/pics/a.jpg" || a.NewPathKey != "pics/a.jpg" {
		t.Fatalf("own filesystem not rewritten: %+v", a)
	}
	if b.NewPath != "/photo/b.jpg" {
		t.Fatalf("another filesystem's targets must not change: %+v", b)
	}
}

func TestCategoryImportReportsSkips(t *testing.T) {
	f := newFixture(t)
	call(t, f.e, "POST", "/api/category/create", fmt.Sprintf(`{"filesystem_id":"%d","name":"已有","path":"/exists"}`, f.fsID))
	body := fmt.Sprintf(`{"filesystem_id":"%d","items":[{"name":"已有","path":"/n1"},{"name":"新","path":"/new"},{"name":"坏","path":"../x"},{"name":"新","path":"/dup"},{"name":"","path":"/e"}]}`, f.fsID)
	status, out := call(t, f.e, "POST", "/api/category/import", body)
	if status != 200 || out["created"] != float64(1) || out["skipped"] != float64(4) {
		t.Fatalf("import: %d %v", status, out)
	}
	_, list := call(t, f.e, "GET", fmt.Sprintf("/api/category/export?filesystem_id=%d", f.fsID), "")
	if len(list["items"].([]any)) != 2 {
		t.Fatalf("export: %v", list)
	}
}

func TestPlanConflictAndApprove(t *testing.T) {
	f := newFixture(t)
	sid := fmt.Sprint(f.session)
	if status, _ := call(t, f.e, "POST", "/api/plan/approve", `{"session_id":"`+sid+`"}`); status != 404 {
		t.Fatalf("approve without plan: %d", status)
	}
	status, body := call(t, f.e, "POST", "/api/plan/update", `{"session_id":"`+sid+`","content":"v1","version":0}`)
	if status != 200 || body["item"].(map[string]any)["version"] != float64(1) {
		t.Fatalf("first save: %d %v", status, body)
	}
	status, body = call(t, f.e, "POST", "/api/plan/update", `{"session_id":"`+sid+`","content":"stale","version":0}`)
	if status != 409 || body["msg"] != "方案已被其他人修改，请刷新后重试" {
		t.Fatalf("stale save: %d %v", status, body)
	}
	status, body = call(t, f.e, "POST", "/api/plan/approve", `{"session_id":"`+sid+`"}`)
	if status != 200 || body["item"].(map[string]any)["status"] != "approved" {
		t.Fatalf("approve: %d %v", status, body)
	}
	_, body = call(t, f.e, "GET", "/api/plan/get?session_id="+sid, "")
	if body["item"].(map[string]any)["review_confidence"] != 0.6 {
		t.Fatalf("plan must carry the review threshold: %v", body)
	}
	if status, _ := call(t, f.e, "GET", "/api/plan/preview?session_id="+sid+"&side=sideways", ""); status != 400 {
		t.Fatalf("bad side: %d", status)
	}
}

func TestReviewConfirmAndOps(t *testing.T) {
	f := newFixture(t)
	addFiles(t, f.session, model.FileEntry{OriginalPath: "a.txt", Name: "a.txt", FileType: "file", NewPath: "/x/a.txt", NewPathKey: "x/a.txt", Operation: "planned", Role: "file", NeedsReview: true, Confidence: 0.4})
	var a model.FileEntry
	container.Default.DB.Take(&a)
	sid := fmt.Sprint(f.session)
	status, body := call(t, f.e, "POST", "/api/plan/confirm_review", fmt.Sprintf(`{"session_id":"%s","file_ids":["%d"]}`, sid, a.ID))
	if status != 200 || body["confirmed"] != float64(1) {
		t.Fatalf("confirm: %d %v", status, body)
	}
	_, body = call(t, f.e, "GET", "/api/plan/ops?session_id="+sid, "")
	if body["total"] != float64(1) {
		t.Fatalf("ops: %v", body)
	}
	status, body = call(t, f.e, "POST", "/api/plan/ops_reset", fmt.Sprintf(`{"session_id":"%s","file_ids":["%d"]}`, sid, a.ID))
	if status != 200 || body["reset"] != float64(1) {
		t.Fatalf("reset: %d %v", status, body)
	}
	if status, _ := call(t, f.e, "POST", "/api/plan/ops_reset", `{"session_id":"`+sid+`","file_ids":["x"]}`); status != 400 {
		t.Fatalf("bad id: %d", status)
	}
}

func TestRuleDuplicateNameIsConflict(t *testing.T) {
	f := newFixture(t)
	sid := fmt.Sprint(f.session)
	mk := `{"session_id":"` + sid + `","name":"图片","exts":"jpg","target":"/图片/{name}"}`
	if status, body := call(t, f.e, "POST", "/api/rule/create", mk); status != 200 {
		t.Fatalf("create: %d %v", status, body)
	}
	if status, body := call(t, f.e, "POST", "/api/rule/create", mk); status != 409 {
		t.Fatalf("duplicate: %d %v", status, body)
	}
	if status, _ := call(t, f.e, "POST", "/api/rule/create", `{"session_id":"`+sid+`","name":"x","target":"/{bogus}"}`); status != 400 {
		t.Fatal("unknown variable must be 400")
	}
	if status, _ := call(t, f.e, "POST", "/api/rule/update", `{"id":"9999","name":"y"}`); status != 404 {
		t.Fatal("unknown rule must be 404")
	}
}

func TestMemoryKeyIsDerivedByTheServer(t *testing.T) {
	f := newFixture(t)
	sid := fmt.Sprint(f.session)
	mk := func(content, path string) map[string]any {
		_, body := call(t, f.e, "POST", "/api/memory/create", fmt.Sprintf(`{"scope":"session","scope_id":"%s","kind":"note","content":%q,"path":%q}`, sid, content, path))
		return body
	}
	first := mk("发票放到财务", "")
	item := first["item"].(map[string]any)
	if item["key"] != "user:发票放到财务" || first["created"] != true {
		t.Fatalf("content key: %v", first)
	}
	if again := mk("发票放到财务", ""); again["created"] != false {
		t.Fatalf("same note must update: %v", again)
	}
	byPath := mk("照片按月份", "/照片")
	if byPath["item"].(map[string]any)["key"] != "path:照片" {
		t.Fatalf("path key: %v", byPath)
	}
	long := mk("一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十", "")
	if k := long["item"].(map[string]any)["key"].(string); len([]rune(k)) != len("user:")+48 {
		t.Fatalf("key must cut at 48 characters: %q", k)
	}
	id := item["id"].(string)
	status, body := call(t, f.e, "POST", "/api/memory/update", fmt.Sprintf(`{"id":"%s","content":"发票放到财务部"}`, id))
	if status != 200 || body["item"].(map[string]any)["key"] != "user:发票放到财务部" {
		t.Fatalf("update re-derives a user note's key: %d %v", status, body)
	}
	if status, _ := call(t, f.e, "POST", "/api/memory/delete", fmt.Sprintf(`{"id":"%s"}`, id)); status != 200 {
		t.Fatal("delete")
	}
	if status, _ := call(t, f.e, "POST", "/api/memory/delete", fmt.Sprintf(`{"id":"%s"}`, id)); status != 200 {
		t.Fatal("deleting twice must succeed")
	}
	if status, _ := call(t, f.e, "GET", "/api/memory/list?scope=bogus", ""); status != 400 {
		t.Fatal("bad scope")
	}
}

func TestChat(t *testing.T) {
	f := newFixture(t)
	sid := fmt.Sprint(f.session)
	if status, body := call(t, f.e, "POST", "/api/chat/send", `{"session_id":"`+sid+`","content":"你好"}`); status != 400 || body["msg"] != "整理任务没有选模型" {
		t.Fatalf("no model: %d %v", status, body)
	}
	if status, _ := call(t, f.e, "POST", "/api/chat/send", `{"session_id":"`+sid+`","content":"  "}`); status != 400 {
		t.Fatalf("blank: %d", status)
	}
	container.Default.DB.Create(&model.ChatTurn{SessionID: f.session, Role: "user", Content: "q"})
	container.Default.DB.Create(&model.ChatTurn{SessionID: f.session, Role: "summary", Content: "s"})
	_, body := call(t, f.e, "GET", "/api/chat/list?session_id="+sid, "")
	if body["total"] != float64(1) {
		t.Fatalf("summary turns are internal: %v", body)
	}
}

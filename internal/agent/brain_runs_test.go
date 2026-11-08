package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

// fakeStep is one streamed answer of fakeModel: text and/or tool calls.
type fakeStep struct {
	text  string
	calls []schema.ToolCall
}

// fakeModel replays scripted streamed steps (agent turns) and Generate
// answers (one-shot calls), recording every input.
type fakeModel struct {
	mu       sync.Mutex
	steps    []fakeStep
	gens     []string
	streamed [][]*schema.Message
	genned   [][]*schema.Message
}

func (m *fakeModel) Generate(_ context.Context, in []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.genned = append(m.genned, in)
	out := "{}"
	if len(m.gens) > 0 {
		out, m.gens = m.gens[0], m.gens[1:]
	}
	return &schema.Message{Role: schema.Assistant, Content: out}, nil
}

func (m *fakeModel) Stream(_ context.Context, in []*schema.Message, _ ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamed = append(m.streamed, in)
	step := fakeStep{text: "完成"}
	if len(m.steps) > 0 {
		step, m.steps = m.steps[0], m.steps[1:]
	}
	msg := &schema.Message{Role: schema.Assistant, Content: step.text, ToolCalls: step.calls,
		ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}}}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *fakeModel) BindTools([]*schema.ToolInfo) error { return nil }

func toolCall(id, name string, args any) schema.ToolCall {
	b, _ := json.Marshal(args)
	idx := 0
	return schema.ToolCall{Index: &idx, ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: string(b)}}
}

// brainAgent sets up a session (id 1, filesystem 1, local fs) and an Agent
// whose runs use model.
func brainAgent(t *testing.T, model *fakeModel) (*db.Repository, *Agent, *db.ScanSession) {
	t.Helper()
	repo := setupToolTestRepo(t)
	old := config.Get()
	config.Update(&config.Config{Agent: config.AgentConfig{MaxStep: 20, ChatMaxStep: 20, MaxRetries: 1}})
	restore := SetModelFactory(func(context.Context, *db.ModelProvider) (einomodel.ChatModel, error) { return model, nil })
	t.Cleanup(func() {
		restore()
		if old != nil {
			config.Update(old)
		}
	})
	session := &db.ScanSession{FilesystemID: 1, RootPath: ".", Status: "scanned", Goal: "照片按年份整理"}
	if err := repo.CreateSession(session); err != nil {
		t.Fatal(err)
	}
	provider := &db.ModelProvider{Name: "fake", Provider: "openai", Model: "fake"}
	a := New(repo, config.RemoteFSConfig{Protocol: "local", BasePath: t.TempDir()}, session.ID, provider, session)
	return repo, a, session
}

func seedEntry(t *testing.T, repo *db.Repository, e db.FileEntry) {
	t.Helper()
	if e.Name == "" {
		e.Name = e.OriginalPath[strings.LastIndex(e.OriginalPath, "/")+1:]
	}
	if err := repo.CreateFile(&e); err != nil {
		t.Fatal(err)
	}
}

func historyBytes(t *testing.T, summary *db.ChatTurn, turns []db.ChatTurn) string {
	t.Helper()
	b, err := json.Marshal(chatHistoryMessages(summary, turns))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "]")
}

// History grows append-only: the rendered history of turn k is a byte
// prefix of turn k+1's, across a fold too, and a fold happens once.
func TestChatHistoryAppendOnly(t *testing.T) {
	repo := setupToolTestRepo(t)
	folds := 0
	summarize := func(_ context.Context, prev string, turns []db.ChatTurn) (string, error) {
		folds++
		return fmt.Sprintf("摘要%d（%d 轮，之前：%s）", folds, len(turns), prev), nil
	}
	add := func(role, content string) {
		if err := repo.AddChatTurn(&db.ChatTurn{SessionID: 1, Role: role, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	build := func() string {
		s, turns, err := repo.ChatHistory(1, 0)
		if err != nil {
			t.Fatal(err)
		}
		s, turns, err = compactChatHistory(context.Background(), repo, 1, 0, s, turns, summarize)
		if err != nil {
			t.Fatal(err)
		}
		return historyBytes(t, s, turns)
	}

	long := strings.Repeat("很长的一段话", 400) // ~1600 tokens per turn
	prev := ""
	foldsAt := []int{}
	for i := 0; i < 20; i++ {
		add(db.ChatRoleUser, fmt.Sprintf("问题 %d %s", i, long))
		add(db.ChatRoleAssistant, fmt.Sprintf("回答 %d", i))
		before := folds
		cur := build()
		if folds == before && prev != "" && !strings.HasPrefix(cur, prev) {
			t.Fatalf("turn %d: history is not an append-only extension of the previous one", i)
		}
		if folds != before {
			foldsAt = append(foldsAt, i)
			// The next turn must extend the folded history.
			prev = cur
			continue
		}
		prev = cur
	}
	if len(foldsAt) == 0 {
		t.Fatal("history over budget must be folded into a summary")
	}
	if len(foldsAt) > 1 && foldsAt[1]-foldsAt[0] < 2 {
		t.Fatalf("folds must not happen on consecutive turns: %v", foldsAt)
	}
	var n int64
	repo.DB().Model(&db.ChatTurn{}).Where("role = ?", db.ChatRoleSummary).Count(&n)
	if int(n) != len(foldsAt) {
		t.Fatalf("summary turns = %d, folds = %d", n, len(foldsAt))
	}
	listed, _ := repo.ListChatTurns(1)
	for _, tr := range listed {
		if tr.Role == db.ChatRoleSummary {
			t.Fatal("summary turns are never listed")
		}
	}
}

func TestRunChatPersistsTurnsAndOrdersPrompt(t *testing.T) {
	model := &fakeModel{steps: []fakeStep{
		{text: "你想按年还是按月？"},
		{calls: []schema.ToolCall{toolCall("c1", "remember", map[string]string{"scope": "global", "kind": "preference", "key": "pref:photo-by-year", "content": "照片按年份放"})}},
		{text: "好的，已记住。"},
	}}
	repo, a, session := brainAgent(t, model)
	seedEntry(t, repo, db.FileEntry{ScanSessionID: session.ID, OriginalPath: "a.jpg", FileType: "file", ParentPath: ".", Size: 2048})
	files, _, _ := repo.ListFiles(db.FileQuery{SessionID: session.ID})

	ctx := context.Background()
	ans, err := a.RunChat(ctx, NewLogger(session.ID, 7, 0), "帮我整理照片", files)
	if err != nil || ans != "你想按年还是按月？" {
		t.Fatalf("turn 1: %q %v", ans, err)
	}
	ans, err = a.RunChat(ctx, NewLogger(session.ID, 8, 0), "按年，以后都这样", nil)
	if err != nil || ans != "好的，已记住。" {
		t.Fatalf("turn 2: %q %v", ans, err)
	}

	turns, _ := repo.ListChatTurns(session.ID)
	want := []string{"user:帮我整理照片", "assistant:你想按年还是按月？", "user:按年，以后都这样", "assistant:好的，已记住。"}
	if len(turns) != len(want) {
		t.Fatalf("turns: %+v", turns)
	}
	for i, tr := range turns {
		if tr.Role+":"+tr.Content != want[i] {
			t.Fatalf("turn %d = %s:%s, want %s", i, tr.Role, tr.Content, want[i])
		}
	}
	if turns[0].RunID != 7 || turns[3].RunID != 8 {
		t.Fatal("turns carry their run id")
	}

	// Turn 1 input: system, shared context, user (with selected files).
	in1 := model.streamed[0]
	if in1[0].Role != schema.System || in1[0].Content != chatPrompt {
		t.Fatal("system prompt first and static")
	}
	if !strings.HasPrefix(in1[1].Content, "# 会话共享上下文") || !strings.Contains(in1[1].Content, "照片按年份整理") {
		t.Fatalf("shared context second: %q", in1[1].Content)
	}
	last := in1[len(in1)-1]
	if !strings.Contains(last.Content, "a.jpg") || !strings.HasSuffix(last.Content, "帮我整理照片") {
		t.Fatalf("selected files then the message last: %q", last.Content)
	}
	// Turn 2 input: the turn 1 prefix is reused, history follows.
	in2 := model.streamed[1]
	if in2[1].Content != in1[1].Content {
		t.Fatal("shared context must be byte-identical across turns")
	}
	if in2[2].Content != "帮我整理照片" || in2[3].Role != schema.Assistant || in2[3].Content != "你想按年还是按月？" {
		t.Fatalf("history after shared context: %+v", in2[2:4])
	}
	mem, _ := repo.ListLongTermMemory(1)
	if len(mem) != 1 || mem[0].Key != "pref:photo-by-year" {
		t.Fatalf("remember wrote %+v", mem)
	}
}

func TestComputeTreeOverview(t *testing.T) {
	repo := setupToolTestRepo(t)
	s := &db.ScanSession{FilesystemID: 1, RootPath: "data"}
	if err := repo.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	for _, e := range []db.FileEntry{
		{OriginalPath: "data/Photos", FileType: "directory"},
		{OriginalPath: "data/Photos/2020", FileType: "directory"},
		{OriginalPath: "data/Photos/2020/a.JPG", FileType: "file", Size: 1000},
		{OriginalPath: "data/Photos/2020/b.jpg", FileType: "file", Size: 1000},
		{OriginalPath: "data/Photos/c.png", FileType: "file", Size: 500},
		{OriginalPath: "data/Code", FileType: "directory"},
		{OriginalPath: "data/Code/proj", FileType: "directory"},
		{OriginalPath: "data/Code/proj/go.mod", FileType: "file", Size: 10},
		{OriginalPath: "data/Code/proj/vendor/x/go.mod", FileType: "file", Size: 10},
		{OriginalPath: "data/readme", FileType: "file", Size: 5},
	} {
		e.ScanSessionID = s.ID
		seedEntry(t, repo, e)
	}
	ov, err := ComputeTreeOverview(repo, s)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Files != 6 || ov.Dirs != 4 || ov.Size != 2525 || ov.MaxDepth != 5 || ov.RootFileCount != 1 {
		t.Fatalf("totals: %+v", ov)
	}
	if ov.TopDirs[0].name != "Photos" || ov.TopDirs[0].files != 3 || ov.TopDirs[0].dirs != 1 {
		t.Fatalf("top dirs: %+v", ov.TopDirs)
	}
	if ov.Exts[0].ext != ".jpg" || ov.Exts[0].count != 2 {
		t.Fatalf("ext histogram is case-folded and sorted: %+v", ov.Exts)
	}
	if len(ov.Markers) != 1 || ov.Markers[0].dir != "Code/proj" {
		t.Fatalf("markers keep the outermost project only: %+v", ov.Markers)
	}
	out := ov.Render()
	for _, w := range []string{"共 6 个文件", "Photos/ — 3 个文件", ".jpg：2 个", "Code/proj：Go 项目", "根目录散文件（1 个）"} {
		if !strings.Contains(out, w) {
			t.Fatalf("render misses %q:\n%s", w, out)
		}
	}
}

func TestRunSurveyWritesPlan(t *testing.T) {
	model := &fakeModel{steps: []fakeStep{
		{calls: []schema.ToolCall{toolCall("c1", "write_plan", map[string]string{"content": "## 目标理解\n照片按年"})}},
		{text: "方案已写好"},
	}}
	repo, a, session := brainAgent(t, model)
	seedEntry(t, repo, db.FileEntry{ScanSessionID: session.ID, OriginalPath: "Photos/a.jpg", FileType: "file"})
	sum, err := a.RunSurvey(context.Background(), NewLogger(session.ID, 3, 0), "按年")
	if err != nil || sum != "方案已写好" {
		t.Fatalf("survey: %q %v", sum, err)
	}
	plan, _ := repo.GetPlan(session.ID)
	if plan.Version != 1 || plan.Status != db.PlanDraft {
		t.Fatalf("plan: %+v", plan)
	}
	in := model.streamed[0]
	user := in[len(in)-1].Content
	if !strings.Contains(user, "## 目录树概览") || !strings.Contains(user, "## 用户补充要求\n\n按年") || in[0].Content != surveyPrompt {
		t.Fatalf("survey input: %q", user)
	}

	// A survey that never writes the plan fails.
	model.steps = []fakeStep{{text: "我看完了"}}
	if _, err := a.RunSurvey(context.Background(), NewLogger(session.ID, 4, 0), ""); err != errNoPlanWritten {
		t.Fatalf("no plan written: %v", err)
	}
}

func TestBuildReviewMaterial(t *testing.T) {
	repo := setupToolTestRepo(t)
	s := &db.ScanSession{FilesystemID: 1, RootPath: "."}
	if err := repo.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	seedCategory(t, repo, 1, "照片", "/照片")
	seedCategory(t, repo, 1, "图片", "/照片/图片")
	seedCategory(t, repo, 1, "文档", "/文档")
	seedCategory(t, repo, 1, "空分类", "/空")
	for _, e := range []db.FileEntry{
		{OriginalPath: "mix/a.jpg", NewPath: "/照片/a.jpg", Reason: "照片"},
		{OriginalPath: "mix/b.png", NewPath: "/照片/图片/b.png"},
		{OriginalPath: "mix/c.pdf", NewPath: "/文档/c.pdf"},
		{OriginalPath: "mix/d.exe", NewPath: "/杂项/d.exe"},
		{OriginalPath: "mix/e.txt"},
	} {
		e.ScanSessionID, e.FileType, e.ParentPath = s.ID, "file", "mix"
		seedEntry(t, repo, e)
	}
	tagging := &db.AgentRun{Summary: "# 报告\n\n## 分类统计\n- x\n\n## 失败批次（1）\n\n- 批次 3（mix）: boom\n"}
	out, err := BuildReviewMaterial(repo, s, tagging)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"### 照片（/照片，共 1 项）", "mix/a.jpg → /照片/a.jpg（照片）",
		"### 图片（/照片/图片，共 1 项）", // longest prefix wins
		"## 没有规划的分类\n\n空分类（/空）",
		"## 不在任何分类下的规划（共 1 项）", "mix/d.exe → /杂项/d.exe",
		"## 同目录文件被分到多个分类", "mix（4 个文件）",
		"图片（/照片/图片）与 照片（/照片）",
		"### 失败批次（1）", "boom", "仍有 5 个文件未处理",
	} {
		if !strings.Contains(out, w) {
			t.Fatalf("material misses %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "分类统计") {
		t.Fatal("only failure sections of the report are carried over")
	}
}

func TestRunReviewSummaryAndReflection(t *testing.T) {
	report := "## 做了什么\n修正 1 项\n\n## 不确定的地方\n无\n\n## 需要你确认的问题\n无"
	model := &fakeModel{
		steps: []fakeStep{{text: report}},
		gens:  []string{"```json\n{\"ops\":[{\"op\":\"ADD\",\"kind\":\"convention\",\"key\":\"path:mix\",\"content\":\"mix 是下载目录\"},{\"op\":\"NOOP\"}]}\n```"},
	}
	repo, a, session := brainAgent(t, model)
	seedEntry(t, repo, db.FileEntry{ScanSessionID: session.ID, OriginalPath: "mix/a.jpg", FileType: "file", NewPath: "/照片/a.jpg"})
	if _, err := repo.UpsertMemory(&db.MemoryItem{Scope: db.MemoryScopeSession, ScopeID: session.ID, Kind: db.MemoryNote, Key: "note:1", Content: "mix 是浏览器下载目录", Source: db.MemorySourceAgent}); err != nil {
		t.Fatal(err)
	}
	sum, err := a.RunReview(context.Background(), NewLogger(session.ID, 9, 0), &db.AgentRun{ID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sum, report) || !strings.Contains(sum, "记忆已更新：新增 1 条") {
		t.Fatalf("summary: %q", sum)
	}
	if in := model.streamed[0]; in[0].Content != reviewPrompt || !strings.Contains(in[len(in)-1].Content, "# 复核材料") {
		t.Fatal("review input: static prompt + material")
	}
	if len(model.genned) != 1 || !strings.Contains(model.genned[0][1].Content, "mix 是浏览器下载目录") {
		t.Fatalf("reflection sees session notes: %+v", model.genned)
	}
	mem, _ := repo.ListLongTermMemory(1)
	if len(mem) != 1 || mem[0].Key != "path:mix" || mem[0].Source != db.MemorySourceReflection {
		t.Fatalf("reflection applied: %+v", mem)
	}
}

func TestReflectionSkippedWithoutMaterial(t *testing.T) {
	model := &fakeModel{}
	_, a, session := brainAgent(t, model)
	res, err := a.Reflect(context.Background(), NewLogger(session.ID, 1, 0), &db.AgentRun{Summary: "# 报告\n"})
	if err != nil || res.Added+res.Updated+res.Deleted != 0 || len(model.genned) != 0 {
		t.Fatalf("nothing to reflect on must skip the model call: %+v %v", res, err)
	}
}

func TestParseMemoryOps(t *testing.T) {
	cases := map[string]int{
		`{"ops":[{"op":"ADD","key":"k","content":"c"},{"op":"DELETE","id":3}]}`: 2,
		"好的：\n```json\n{\"ops\": []}\n```":                                      0,
		`[{"op":"NOOP"},{"op":"UPDATE","id":1,"content":"x"}]`:                  2,
	}
	for in, n := range cases {
		ops, err := ParseMemoryOps(in)
		if err != nil || len(ops) != n {
			t.Fatalf("%q → %v %v", in, ops, err)
		}
	}
	ops, _ := ParseMemoryOps(`{"ops":[{"op":"DELETE","id":3}]}`)
	if ops[0].Op != MemoryOpDelete || ops[0].ID != 3 {
		t.Fatalf("fields: %+v", ops[0])
	}
	if _, err := ParseMemoryOps("没有 JSON"); err == nil {
		t.Fatal("non-JSON answers are errors")
	}
}

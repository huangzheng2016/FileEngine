package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/agent"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

type stepModel struct{ steps []*schema.Message }

func (m *stepModel) next() *schema.Message {
	if len(m.steps) == 0 {
		return &schema.Message{Role: schema.Assistant, Content: "完成"}
	}
	s := m.steps[0]
	m.steps = m.steps[1:]
	return s
}

func (m *stepModel) Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant, Content: `{"ops":[]}`}, nil
}

func (m *stepModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{m.next()}), nil
}

func (m *stepModel) BindTools([]*schema.ToolInfo) error { return nil }

func chatServer(t *testing.T, model *stepModel) (*Server, *db.Repository) {
	t.Helper()
	s, repo := brainServer(t)
	mp := &db.ModelProvider{Name: "fake-chat", Provider: "openai", Model: "fake"}
	if err := repo.CreateModelProvider(mp); err != nil {
		t.Fatal(err)
	}
	if err := repo.DB().Model(&db.ScanSession{}).Where("id = 1").Update("model_provider_id", mp.ID).Error; err != nil {
		t.Fatal(err)
	}
	old := config.Get()
	config.Update(&config.Config{Agent: config.AgentConfig{MaxStep: 20, ChatMaxStep: 20, MaxRetries: 1}})
	t.Cleanup(func() {
		if old != nil {
			config.Update(old)
		}
	})
	t.Cleanup(agent.SetModelFactory(func(context.Context, *db.ModelProvider) (einomodel.ChatModel, error) { return model, nil }))
	return s, repo
}

func waitRun(t *testing.T, repo *db.Repository, id uint) *db.AgentRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := repo.GetRun(id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != db.RunRunning {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %d still running", id)
	return nil
}

func runIDOf(t *testing.T, body map[string]any) uint {
	t.Helper()
	id, ok := body["run_id"].(float64)
	if !ok {
		t.Fatalf("no run_id in %v", body)
	}
	return uint(id)
}

func TestChatEndpoints(t *testing.T) {
	s, repo := chatServer(t, &stepModel{steps: []*schema.Message{{Role: schema.Assistant, Content: "你好，要怎么整理？"}}})

	if code, _ := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/chat", map[string]any{"content": "  "}); code != http.StatusBadRequest {
		t.Fatalf("empty message: %d", code)
	}
	code, body := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/chat", map[string]any{"content": "整理一下设计稿", "file_ids": []uint{2, 3}})
	if code != http.StatusOK {
		t.Fatalf("post: %d %v", code, body)
	}
	run := waitRun(t, repo, runIDOf(t, body))
	if run.Phase != db.RunPhaseChat || run.Status != db.RunDone || run.Summary != "你好，要怎么整理？" || run.Title != "整理一下设计稿" {
		t.Fatalf("chat run: %+v", run)
	}

	code, list := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/1/chat", nil)
	items, _ := list["items"].([]any)
	if code != http.StatusOK || len(items) != 2 {
		t.Fatalf("list: %d %v", code, list)
	}
	first, second := items[0].(map[string]any), items[1].(map[string]any)
	if first["role"] != "user" || first["content"] != "整理一下设计稿" || second["role"] != "assistant" || second["run_id"].(float64) != float64(run.ID) {
		t.Fatalf("turns: %v", items)
	}
	if _, leaked := first["covers_through"]; leaked {
		t.Fatal("covers_through is internal")
	}
}

func TestChatAndDraftBusy(t *testing.T) {
	s, repo := chatServer(t, &stepModel{})
	busy, err := repo.CreateRun(1, db.RunPhaseTagging, "自动整理")
	if err != nil {
		t.Fatal(err)
	}
	if code, body := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/chat", map[string]any{"content": "hi"}); code != http.StatusConflict {
		t.Fatalf("chat while busy: %d %v", code, body)
	}
	if code, body := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/plan/draft", nil); code != http.StatusConflict {
		t.Fatalf("draft while busy: %d %v", code, body)
	}
	turns, _ := repo.ListChatTurns(1)
	if len(turns) != 0 {
		t.Fatal("a refused chat persists nothing")
	}
	_ = repo.FinishRun(busy, db.RunDone, "", "", db.TokenCounts{})
}

func TestDraftPlanEndpoint(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"content": "## 目标理解\n按类型整理"})
	idx := 0
	model := &stepModel{steps: []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &idx, ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "write_plan", Arguments: string(args)}}}},
		{Role: schema.Assistant, Content: "方案已生成"},
	}}
	s, repo := chatServer(t, model)
	code, body := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions/1/plan/draft", map[string]any{"instructions": "设计稿单独放"})
	if code != http.StatusOK {
		t.Fatalf("draft: %d %v", code, body)
	}
	run := waitRun(t, repo, runIDOf(t, body))
	if run.Phase != db.RunPhaseSurvey || run.Status != db.RunDone || run.Summary != "方案已生成" {
		t.Fatalf("survey run: %+v", run)
	}
	plan, _ := repo.GetPlan(1)
	if plan.Version != 1 || plan.Content != "## 目标理解\n按类型整理" {
		t.Fatalf("plan: %+v", plan)
	}
	events, _, err := repo.ListEvents(db.EventQuery{SessionID: 1, RunID: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		found = found || e.Type == db.EvPlanUpdated
	}
	if !found {
		t.Fatal("survey emits plan_updated")
	}
}

func TestReviewChainsAfterTagging(t *testing.T) {
	s, repo := chatServer(t, &stepModel{steps: []*schema.Message{{Role: schema.Assistant, Content: "## 做了什么\n未发现需要修正的问题"}}})
	tagging, err := repo.CreateRun(1, db.RunPhaseTagging, "自动整理")
	if err != nil {
		t.Fatal(err)
	}
	_ = repo.FinishRun(tagging, db.RunStopped, "", "", db.TokenCounts{})
	s.startReviewAfterTagging(1, tagging.ID, db.RunStopped)
	if runs, _ := repo.ListRuns(1); len(runs) != 1 {
		t.Fatal("a stopped tagging run is not reviewed")
	}

	s.startReviewAfterTagging(1, tagging.ID, db.RunDone)
	runs, _ := repo.ListRuns(1)
	if len(runs) != 2 || runs[0].Phase != db.RunPhaseReview {
		t.Fatalf("review run started: %+v", runs)
	}
	review := waitRun(t, repo, runs[0].ID)
	if review.Status != db.RunDone || review.Summary != "## 做了什么\n未发现需要修正的问题" {
		t.Fatalf("review run: %+v", review)
	}
}

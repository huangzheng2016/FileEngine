package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"

	"FileEngine/internal/agent"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func TestCreateSessionRequiresModel(t *testing.T) {
	s := setupSessionTestServer(t)
	for _, body := range []map[string]any{
		{"filesystem_id": 1},
		{"filesystem_id": 1, "model_provider_id": 99},
		{"filesystem_id": 1, "model_provider_id": 1, "bulk_model_provider_id": 99},
	} {
		code, resp := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", body)
		if code != http.StatusBadRequest || resp["error"] != msgPickModel {
			t.Fatalf("create %v: %d %v", body, code, resp)
		}
	}
	if n, _ := s.repo.ListSessions(); len(n) != 0 {
		t.Fatalf("rejected creates must not leave sessions: %d", len(n))
	}
}

func TestDeleteStorageOrModelInUse(t *testing.T) {
	s := setupSessionTestServer(t)
	bulk := &db.ModelProvider{Name: "bulk", Provider: "openai", Model: "fake"}
	if err := s.repo.CreateModelProvider(bulk); err != nil {
		t.Fatal(err)
	}
	sess := &db.ScanSession{FilesystemID: 1, ModelProviderID: 1, BulkModelProviderID: bulk.ID, Status: "scanned"}
	if err := s.repo.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ path, msg string }{
		{"/api/v1/filesystems/1", "有 1 个整理任务在用这个存储，先删除这些任务"},
		{"/api/v1/models/1", "有 1 个整理任务在用这个模型，先删除这些任务"},
		{"/api/v1/models/" + itoa(bulk.ID), "有 1 个整理任务在用这个模型，先删除这些任务"},
	}
	for _, tc := range cases {
		if code, resp := doModelRequest(t, s, http.MethodDelete, tc.path, nil); code != http.StatusConflict || resp["error"] != tc.msg {
			t.Fatalf("delete %s: %d %v", tc.path, code, resp)
		}
	}
	if code, resp := doModelRequest(t, s, http.MethodDelete, "/api/v1/sessions/"+itoa(sess.ID), nil); code != http.StatusOK {
		t.Fatalf("delete session: %d %v", code, resp)
	}
	for _, tc := range cases {
		if code, resp := doModelRequest(t, s, http.MethodDelete, tc.path, nil); code != http.StatusOK {
			t.Fatalf("delete %s after its task: %d %v", tc.path, code, resp)
		}
	}
}

func TestCreateWithAutoTagStartsTaggingAfterScan(t *testing.T) {
	s := setupSessionTestServer(t)
	old := config.Get()
	config.Update(&config.Config{Agent: config.AgentConfig{MaxStep: 20, ChatMaxStep: 20, MaxRetries: 1}})
	t.Cleanup(func() {
		if old != nil {
			config.Update(old)
		}
	})
	model := &stepModel{}
	t.Cleanup(agent.SetModelFactory(func(context.Context, *db.ModelProvider) (einomodel.ChatModel, error) { return model, nil }))

	code, created := doModelRequest(t, s, http.MethodPost, "/api/v1/sessions", map[string]any{
		"filesystem_id": 1, "model_provider_id": 1, "auto_tag": true,
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, created)
	}
	id := uint(created["id"].(float64))

	var tagging *db.AgentRun
	deadline := time.Now().Add(5 * time.Second)
	for tagging == nil && time.Now().Before(deadline) {
		runs, err := s.repo.ListRuns(id)
		if err != nil {
			t.Fatal(err)
		}
		for i := range runs {
			if runs[i].Phase == db.RunPhaseTagging {
				tagging = &runs[i]
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tagging == nil {
		t.Fatal("no tagging run after the scan")
	}
	if sess, _ := s.repo.GetSession(id); sess.Status != "scanned" {
		t.Fatalf("tagging started before the scan completed: %s", sess.Status)
	}
	if tagging.Title != "整理" {
		t.Fatalf("tagging title = %q", tagging.Title)
	}
	waitSessionIdle(t, s, id)

	code, got := doModelRequest(t, s, http.MethodGet, "/api/v1/sessions/"+itoa(id), nil)
	latest, _ := got["latest_run"].(map[string]any)
	if code != http.StatusOK || latest == nil || latest["phase"] == nil {
		t.Fatalf("detail latest_run: %d %v", code, got)
	}
	if _, ok := got["review_count"]; !ok {
		t.Fatalf("detail misses review_count: %v", got)
	}
}

// Waits until the tagging run and the review it chains have both finished.
func waitSessionIdle(t *testing.T, s *Server, id uint) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runs, _ := s.repo.ListRuns(id)
		idle, reviewed, chains := true, false, false
		for _, r := range runs {
			if r.Status == db.RunRunning {
				idle = false
			}
			if r.Phase == db.RunPhaseReview {
				reviewed = true
			}
			if r.Phase == db.RunPhaseTagging && (r.Status == db.RunDone || r.Status == db.RunPartial) {
				chains = true
			}
		}
		if idle && reviewed == chains && len(runs) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %d runs did not settle", id)
}

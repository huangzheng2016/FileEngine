package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func setupEventsTestServer(t *testing.T) (*Server, *db.Repository) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := db.Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("db init: %v", err)
	}
	repo := db.NewRepository(db.GetDB())
	return NewServer(repo, nil), repo
}

func createTestSession(t *testing.T, repo *db.Repository) *db.ScanSession {
	t.Helper()
	fs := &db.Filesystem{Name: fmt.Sprintf("fs-%d", time.Now().UnixNano()), Protocol: "local", BasePath: t.TempDir()}
	if err := repo.CreateFilesystem(fs); err != nil {
		t.Fatal(err)
	}
	s := &db.ScanSession{FilesystemID: fs.ID, Status: "scanned"}
	if err := repo.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	return s
}

type sseFrame struct {
	ID   uint
	Type string
	Data map[string]any
}

// openStream connects to the SSE endpoint and returns a frame channel.
func openStream(t *testing.T, srv *httptest.Server, path, lastEventID string) <-chan sseFrame {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d", resp.StatusCode)
	}
	out := make(chan sseFrame, 64)
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		var f sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				n, _ := strconv.ParseUint(strings.TrimPrefix(line, "id: "), 10, 64)
				f.ID = uint(n)
			case strings.HasPrefix(line, "event: "):
				f.Type = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f.Data)
			case line == "" && f.Type != "":
				out <- f
				f = sseFrame{}
			}
		}
	}()
	return out
}

func nextFrame(t *testing.T, ch <-chan sseFrame) sseFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SSE frame")
	}
	return sseFrame{}
}

func TestStreamSessionEventsUnknownSession(t *testing.T) {
	s, _ := setupEventsTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/999/events/stream", nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", w.Code)
	}
}

// Last-Event-ID resumes after the given seq: persisted events are replayed
// in order, then live events follow without duplicates.
func TestStreamSessionEventsReplayLastEventID(t *testing.T) {
	s, repo := setupEventsTestServer(t)
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	sess := createTestSession(t, repo)

	run, err := repo.CreateRun(sess.ID, db.RunPhaseTagging, "整理")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		db.Emit(sess.ID, run.ID, db.EvProgress, db.ProgressData{RunID: run.ID, Phase: db.RunPhaseTagging, Total: i})
	}
	all, _, _ := repo.ListEvents(db.EventQuery{SessionID: sess.ID})
	if len(all) != 4 {
		t.Fatalf("persisted %d events, want 4", len(all))
	}

	frames := openStream(t, srv, fmt.Sprintf("/api/v1/sessions/%d/events/stream", sess.ID), strconv.Itoa(int(all[1].ID)))
	for _, want := range all[2:] {
		f := nextFrame(t, frames)
		if f.ID != want.ID || f.Type != want.Type {
			t.Fatalf("replayed frame %d/%s, want %d/%s", f.ID, f.Type, want.ID, want.Type)
		}
	}

	db.Emit(sess.ID, run.ID, db.EvText, db.DeltaData{RunID: run.ID, MsgID: "m1", Delta: "live"})
	db.FlushEvents()
	f := nextFrame(t, frames)
	if f.Type != db.EvText || f.ID <= all[3].ID || f.Data["delta"] != "live" {
		t.Fatalf("live frame = %+v", f)
	}
}

// Without a resume point the stream starts at the latest run's first event.
func TestStreamSessionEventsDefaultsToLatestRun(t *testing.T) {
	s, repo := setupEventsTestServer(t)
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	sess := createTestSession(t, repo)

	first, _ := repo.CreateRun(sess.ID, db.RunPhaseTagging, "第一次")
	_ = repo.FinishRun(first, db.RunDone, "ok", "", db.TokenCounts{})
	second, _ := repo.CreateRun(sess.ID, db.RunPhaseExecuting, "第二次")

	frames := openStream(t, srv, fmt.Sprintf("/api/v1/sessions/%d/events/stream", sess.ID), "")
	f := nextFrame(t, frames)
	if f.Type != db.EvRunStatus || f.Data["run_id"] != float64(second.ID) || f.Data["status"] != db.RunRunning {
		t.Fatalf("first frame = %+v, want run_status of run %d", f, second.ID)
	}

	// ?run_id= restricts the stream to one run.
	byRun := openStream(t, srv, fmt.Sprintf("/api/v1/sessions/%d/events/stream?run_id=%d", sess.ID, first.ID), "")
	for _, want := range []string{db.EvRunStatus, db.EvRunStatus, db.EvRunDone} {
		f := nextFrame(t, byRun)
		if f.Type != want || f.Data["run_id"] != float64(first.ID) {
			t.Fatalf("run-filtered frame = %+v, want %s of run %d", f, want, first.ID)
		}
	}
}

func TestListSessionEvents(t *testing.T) {
	s, repo := setupEventsTestServer(t)
	sess := createTestSession(t, repo)
	run, _ := repo.CreateRun(sess.ID, db.RunPhaseChat, "对话")
	db.Emit(sess.ID, run.ID, db.EvMessage, db.MessageData{RunID: run.ID, MsgID: "m1", Role: "user", Content: "你好"})

	code, body := doModelRequest(t, s, http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/events?run_id=%d&limit=1", sess.ID, run.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("code %d: %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 || body["has_more"] != true {
		t.Fatalf("page = %v", body)
	}
	first := items[0].(map[string]any)
	data, _ := first["data"].(map[string]any)
	if first["type"] != db.EvRunStatus || data["run_id"] != float64(run.ID) || first["seq"] == nil {
		t.Fatalf("item = %v", first)
	}

	code, body = doModelRequest(t, s, http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/events?after=%v", sess.ID, first["seq"]), nil)
	items, _ = body["items"].([]any)
	if code != http.StatusOK || len(items) != 1 || body["has_more"] != false {
		t.Fatalf("after page = %v", body)
	}
	if d := items[0].(map[string]any)["data"].(map[string]any); d["content"] != "你好" {
		t.Fatalf("message item = %v", items[0])
	}
}

// A session with a running run refuses to start another one (409), and an
// orphaned running run can be stopped through the runs API.
func TestRunBusyAndStop(t *testing.T) {
	s, repo := setupEventsTestServer(t)
	sess := createTestSession(t, repo)
	orphan, err := repo.CreateRun(sess.ID, db.RunPhaseTagging, "整理")
	if err != nil {
		t.Fatal(err)
	}

	code, body := doModelRequest(t, s, http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/execute", sess.ID), map[string]any{"mode": "copy"})
	if code != http.StatusConflict || body["error"] != db.ErrSessionBusy.Error() {
		t.Fatalf("busy session: code %d body %v, want 409", code, body)
	}

	code, body = doModelRequest(t, s, http.MethodPost, fmt.Sprintf("/api/v1/runs/%d/stop", orphan.ID), nil)
	if code != http.StatusOK || body["status"] != db.RunStopped {
		t.Fatalf("stop orphan: code %d body %v", code, body)
	}

	code, body = doModelRequest(t, s, http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/runs", sess.ID), nil)
	items, _ := body["items"].([]any)
	if code != http.StatusOK || len(items) != 1 {
		t.Fatalf("list runs: code %d body %v", code, body)
	}
}

// launchRun runs work on a stoppable context; POST /runs/:id/stop cancels it
// and answers once the run is stopped.
func TestLaunchRunStop(t *testing.T) {
	s, repo := setupEventsTestServer(t)
	sess := createTestSession(t, repo)
	run, err := s.launchRun(sess.ID, db.RunPhaseTagging, "整理", func(ctx context.Context, run *db.AgentRun) runOutcome {
		<-ctx.Done()
		return runOutcome{Err: ctx.Err(), Tokens: db.TokenCounts{Total: 3}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.launchRun(sess.ID, db.RunPhaseChat, "对话", func(context.Context, *db.AgentRun) runOutcome { return runOutcome{} }); err != db.ErrSessionBusy {
		t.Fatalf("second launch: err = %v, want ErrSessionBusy", err)
	}

	code, body := doModelRequest(t, s, http.MethodPost, fmt.Sprintf("/api/v1/runs/%d/stop", run.ID), nil)
	if code != http.StatusOK || body["status"] != db.RunStopped || body["total_tokens"] != float64(3) {
		t.Fatalf("stop: code %d body %v", code, body)
	}

	code, body = doModelRequest(t, s, http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d", sess.ID), nil)
	tokens, _ := body["tokens"].(map[string]any)
	if code != http.StatusOK || tokens["total"] != float64(3) || body["status"] != "scanned" {
		t.Fatalf("session: code %d body %v", code, body)
	}
}

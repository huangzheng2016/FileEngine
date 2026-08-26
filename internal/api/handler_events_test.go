package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func TestStreamSessionEventsUnknownSession(t *testing.T) {
	s, _ := setupEventsTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/999/events/stream", nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", w.Code)
	}
}

func TestStreamSessionEventsStreamsPublishedEvents(t *testing.T) {
	s, repo := setupEventsTestServer(t)

	if err := repo.CreateSession(&db.ScanSession{Status: "tagging"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/1/events/stream", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Router().ServeHTTP(w, req)
	}()

	// Publish repeatedly for a short window so at least one event lands after
	// the handler subscribed (duplicates are harmless for the assertions).
	for i := 0; i < 10; i++ {
		db.PublishEvent(db.Event{Type: db.EventProgress, SessionID: 1, Payload: db.ProgressPayload{
			SessionID: 1, Phase: db.PhaseTagging, Total: 10, Tagged: 7, Planned: 3,
		}})
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	<-done

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type: got %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, ": session 1 event stream") {
		t.Fatalf("greeting missing, body: %q", body)
	}
	if !strings.Contains(body, "event: progress\n") {
		t.Fatalf("progress event missing, body: %q", body)
	}
	if !strings.Contains(body, `"tagged":7`) || !strings.Contains(body, `"planned":3`) {
		t.Fatalf("progress payload missing, body: %q", body)
	}
}

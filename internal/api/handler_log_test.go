package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func setupLogTestServer(t *testing.T) (*Server, *db.Repository) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := db.Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("db init: %v", err)
	}
	repo := db.NewRepository(db.GetDB())
	return NewServer(repo, nil), repo
}

func getLogs(t *testing.T, s *Server, query string) (int, []map[string]any, float64) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs?"+query, nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		return w.Code, nil, 0
	}
	var resp struct {
		Logs  []map[string]any `json:"logs"`
		Total float64          `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return w.Code, resp.Logs, resp.Total
}

// GET /logs worker_id/before_id filters: worker_id=0 is a legal filter value
// (pre-lane rows), before_id is a strict upper bound, and total (COUNT) always
// carries the same filters as the row query.
func TestListLogsWorkerAndBeforeIDFilters(t *testing.T) {
	s, repo := setupLogTestServer(t)

	// Session 1: two rows per worker lane 0/1/2; session 2: one lane-0 row.
	seed := []*db.AgentLog{}
	for w := 0; w < 3; w++ {
		for i := 0; i < 2; i++ {
			seed = append(seed, &db.AgentLog{
				ScanSessionID: 1, WorkerID: w, Role: "assistant",
				Content: fmt.Sprintf("w%d-%d", w, i),
			})
		}
	}
	seed = append(seed, &db.AgentLog{ScanSessionID: 2, WorkerID: 0, Role: "assistant", Content: "other-session"})
	if err := repo.CreateAgentLogs(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, l := range seed {
		if l.ID == 0 {
			t.Fatalf("seed row not ID-backfilled: %+v", l)
		}
	}

	// worker_id filter, including the legal 0 lane.
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"session_id=1&worker_id=0", 2},
		{"session_id=1&worker_id=1", 2},
		{"session_id=1&worker_id=2", 2},
		{"session_id=1&worker_id=9", 0},
		{"session_id=1", 6}, // no worker filter
	} {
		code, logs, total := getLogs(t, s, tc.query)
		if code != http.StatusOK {
			t.Fatalf("%s: code %d", tc.query, code)
		}
		if len(logs) != tc.want || int(total) != tc.want {
			t.Fatalf("%s: got %d logs total=%v, want %d", tc.query, len(logs), total, tc.want)
		}
	}
	// Filtered rows really carry the requested lane.
	code, logs, _ := getLogs(t, s, "session_id=1&worker_id=2")
	if code != http.StatusOK {
		t.Fatalf("worker_id=2: code %d", code)
	}
	for _, l := range logs {
		if l["worker_id"] != float64(2) {
			t.Fatalf("worker_id=2 returned row with worker_id=%v", l["worker_id"])
		}
	}

	// before_id is a strict upper bound: rows with id < before_id only.
	pivot := seed[3].ID // 4th seeded row (lane 1)
	code, logs, total := getLogs(t, s, fmt.Sprintf("session_id=1&before_id=%d", pivot))
	if code != http.StatusOK {
		t.Fatalf("before_id: code %d", code)
	}
	if int(total) != 3 || len(logs) != 3 {
		t.Fatalf("before_id=%d: got %d logs total=%v, want 3", pivot, len(logs), total)
	}
	for _, l := range logs {
		if uint(l["id"].(float64)) >= pivot {
			t.Fatalf("before_id=%d returned row id=%v", pivot, l["id"])
		}
	}

	// before_id composes with worker_id; COUNT matches the row set.
	code, logs, total = getLogs(t, s, fmt.Sprintf("session_id=1&worker_id=0&before_id=%d", pivot))
	if code != http.StatusOK {
		t.Fatalf("worker+before: code %d", code)
	}
	if int(total) != 2 || len(logs) != 2 {
		t.Fatalf("worker_id=0&before_id=%d: got %d logs total=%v, want 2", pivot, len(logs), total)
	}

	// desc returns newest-first within the bound; asc (default) oldest-first.
	code, desc, _ := getLogs(t, s, fmt.Sprintf("session_id=1&before_id=%d&order=desc", pivot))
	if code != http.StatusOK || len(desc) != 3 {
		t.Fatalf("desc: code %d logs %d", code, len(desc))
	}
	if desc[0]["id"].(float64) != float64(pivot-1) {
		t.Fatalf("desc first row: got id %v, want %d", desc[0]["id"], pivot-1)
	}
	code, asc, _ := getLogs(t, s, fmt.Sprintf("session_id=1&before_id=%d&order=asc", pivot))
	if code != http.StatusOK || len(asc) != 3 {
		t.Fatalf("asc: code %d logs %d", code, len(asc))
	}
	if asc[0]["id"].(float64) != float64(seed[0].ID) {
		t.Fatalf("asc first row: got id %v, want %d", asc[0]["id"], seed[0].ID)
	}

	// Invalid params are rejected; absent params keep legacy behavior (200).
	for _, q := range []string{
		"session_id=1&worker_id=-1",
		"session_id=1&worker_id=abc",
		"session_id=1&before_id=-1",
		"session_id=1&before_id=abc",
		"session_id=1&before_id=1.5",
	} {
		code, _, _ := getLogs(t, s, q)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: got code %d, want 400", q, code)
		}
	}
}

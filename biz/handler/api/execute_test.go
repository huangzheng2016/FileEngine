package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestExecuteCheckLocateAndRevertErrors(t *testing.T) {
	f := newSessionFixture(t)
	item := f.createSession(t, "")
	id := item["id"].(string)
	f.waitScanned(t, id)

	code, out := f.call(t, http.MethodGet, "/api/execute/check?session_id="+id, "")
	if code != http.StatusOK || out["total"].(float64) != 0 {
		t.Fatalf("check: %d %v", code, out)
	}
	if code, out = f.call(t, http.MethodGet, "/api/execute/check?session_id="+id+"&mode=zip", ""); code != http.StatusBadRequest || out["msg"] != "执行模式只能是 copy 或 move" {
		t.Fatalf("bad mode: %d %v", code, out)
	}
	if code, out = f.call(t, http.MethodGet, "/api/execute/locate?session_id="+id+"&q=a.txt", ""); code != http.StatusOK || out["total"].(float64) != 1 {
		t.Fatalf("locate: %d %v", code, out)
	}
	if code, _ = f.call(t, http.MethodGet, "/api/execute/locate?session_id="+id+"&q=", ""); code != http.StatusBadRequest {
		t.Fatalf("empty locate query: %d", code)
	}
	if code, _ = f.call(t, http.MethodPost, "/api/execute/revert", `{"run_id":"999"}`); code != http.StatusNotFound {
		t.Fatalf("revert of a missing run: %d", code)
	}
	if code, out = f.call(t, http.MethodPost, "/api/execute/start", fmt.Sprintf(`{"session_id":"%s","mode":"copy"}`, id)); code != http.StatusOK {
		t.Fatalf("execute: %d %v", code, out)
	}
	runID := out["run_id"].(string)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, got := f.call(t, http.MethodGet, "/api/run/get?run_id="+runID, "")
		if run := got["item"].(map[string]any); run["status"] != "running" {
			if run["status"] != "done" || run["exec_total"].(float64) != 0 {
				t.Fatalf("finished run = %v", run)
			}
			if code, out = f.call(t, http.MethodPost, "/api/execute/revert", fmt.Sprintf(`{"run_id":"%s"}`, runID)); code != http.StatusOK {
				t.Fatalf("revert: %d %v", code, out)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("execute run did not finish")
}

func TestListEventsOfASession(t *testing.T) {
	f := newSessionFixture(t)
	item := f.createSession(t, "")
	id := item["id"].(string)
	f.waitScanned(t, id)
	if code, out := f.call(t, http.MethodGet, "/api/event/list?session_id="+id+"&limit=-1", ""); code != http.StatusBadRequest {
		t.Fatalf("negative limit: %d %v", code, out)
	}
	if code, out := f.call(t, http.MethodGet, "/api/event/list?session_id="+id, ""); code != http.StatusOK {
		t.Fatalf("list: %d %v", code, out)
	}
	if code, _ := f.call(t, http.MethodGet, "/api/event/stream?session_id=999", ""); code != http.StatusNotFound {
		t.Fatalf("stream of a missing session: %d", code)
	}
}

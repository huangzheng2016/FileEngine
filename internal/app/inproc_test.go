package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/db"
)

func TestDoIsTrustedAndRoutesLikeHTTP(t *testing.T) {
	a := newTestApp(t)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })

	res := a.Do(context.Background(), Call{Method: "GET", Path: "/api/auth/me"})
	var me map[string]any
	if err := json.Unmarshal(res.Body, &me); err != nil || res.Status != 200 || me["desktop"] != true {
		t.Fatalf("me = %d %s", res.Status, res.Body)
	}
	// the same route table as HTTP
	if res := a.Do(context.Background(), Call{Method: "GET", Path: "/api/session/list", Query: map[string]string{"filesystem_id": "1"}}); res.Status != 200 {
		t.Fatalf("session list = %d %s", res.Status, res.Body)
	}
	if res := a.Do(context.Background(), Call{Method: "POST", Path: "/api/session/create", Body: []byte(`{"filesystem_id":"1"}`)}); res.Status != 400 || !strings.Contains(string(res.Body), "存储不存在") {
		t.Fatalf("POST create = %d %s", res.Status, res.Body)
	}
	if res := a.Do(context.Background(), Call{Method: "POST", Path: "/api/session/create", Body: []byte(`{"filesystem_id":`)}); res.Status != 400 {
		t.Fatalf("bad body = %d %s", res.Status, res.Body)
	}
	for _, c := range []Call{{Method: "GET", Path: "/healthz"}, {Method: "GET", Path: "/api/event/stream"}} {
		if res := a.Do(context.Background(), c); res.Status < 400 {
			t.Fatalf("%s must be refused, got %d", c.Path, res.Status)
		}
	}
}

func TestDesktopMarkerCannotBeSetOverHTTP(t *testing.T) {
	a := newTestApp(t)
	base := serve(t, a)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	req, _ := http.NewRequest("GET", base+"/api/session/list", nil)
	req.Header.Set("X-Desktop", "1")
	req.Host = "fileengine.desktop"
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 && resp.StatusCode != 401 {
		t.Fatalf("forged desktop call = %d", resp.StatusCode)
	}
}

func TestFollowResumesWithoutGapsAndStopsAtShutdown(t *testing.T) {
	a := newTestApp(t)
	sid := newSession(t)
	db.Emit(sid, 1, db.EvProgress, db.ProgressData{RunID: 1, Phase: "tagging", Total: 1})

	got := make(chan model.SessionEvent, 16)
	ended := make(chan error, 1)
	if _, err := a.Follow(sid, 0, nil, func(ev model.SessionEvent) error { got <- ev; return nil }, func(err error) { ended <- err }); err != nil {
		t.Fatal(err)
	}
	db.Emit(sid, 1, db.EvSessionUpdated, struct{}{})
	var seqs []uint
	for len(seqs) < 2 {
		select {
		case ev := <-got:
			seqs = append(seqs, ev.ID)
		case <-time.After(2 * time.Second):
			t.Fatalf("got %v", seqs)
		}
	}
	if seqs[1] <= seqs[0] {
		t.Fatalf("out of order: %v", seqs)
	}

	// resume after the first: only the second arrives
	after := seqs[0]
	got2 := make(chan model.SessionEvent, 16)
	if _, err := a.Follow(sid, 0, &after, func(ev model.SessionEvent) error { got2 <- ev; return nil }, func(error) {}); err != nil {
		t.Fatal(err)
	}
	if ev := <-got2; ev.ID != seqs[1] {
		t.Fatalf("resumed at %d, want %d", ev.ID, seqs[1])
	}

	if _, err := a.Follow(99999, 0, nil, nil, nil); err == nil {
		t.Fatal("unknown session must fail")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		if err != nil {
			t.Fatalf("follower ended with %v", err)
		}
	default:
		t.Fatal("follower still running after Shutdown")
	}
}

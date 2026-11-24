package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"FileEngine/container"
	"FileEngine/internal/app"
	"FileEngine/internal/db"
)

type capture struct {
	mu     sync.Mutex
	events []EventPayload
	ends   []EndPayload
	ch     chan struct{}
}

func newCapture() *capture { return &capture{ch: make(chan struct{}, 64)} }

func (c *capture) emit(topic string, payload any) {
	c.mu.Lock()
	switch p := payload.(type) {
	case EventPayload:
		c.events = append(c.events, p)
	case EndPayload:
		c.ends = append(c.ends, p)
	}
	c.mu.Unlock()
	c.ch <- struct{}{}
}

func (c *capture) wait(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-c.ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for notification %d of %d", i+1, n)
		}
	}
}

func newService(t *testing.T) (*Service, *capture) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "database:\n  driver: sqlite\n  dsn: " + filepath.ToSlash(filepath.Join(dir, "fe.db")) + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := app.New(app.Options{
		ConfigPath: cfgPath,
		Frontend:   fstest.MapFS{"index.html": {Data: []byte("<title>x</title>")}},
		Desktop:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cap := newCapture()
	s := &Service{app: a, emit: cap.emit}
	t.Cleanup(func() { _ = s.ServiceShutdown() })
	return s, cap
}

func TestCallRunsTheIDLRoutes(t *testing.T) {
	s, _ := newService(t)
	res := s.Call(t.Context(), CallRequest{Method: "get", Path: "/api/auth/me"})
	var me struct {
		Authenticated bool `json:"authenticated"`
		Desktop       bool `json:"desktop"`
	}
	if res.Status != 200 || res.Encoding != "utf8" || json.Unmarshal([]byte(res.Body), &me) != nil || !me.Authenticated || !me.Desktop {
		t.Fatalf("me = %+v", res)
	}
	res = s.Call(t.Context(), CallRequest{Method: "GET", Path: "/api/event/stream", Query: map[string]string{"session_id": "1"}})
	if res.Status != 400 {
		t.Fatalf("stream route = %+v, want refused", res)
	}
	res = s.Call(t.Context(), CallRequest{Method: "GET", Path: "/healthz"})
	if res.Status != 404 {
		t.Fatalf("non-api path = %+v", res)
	}
	if isTextual("application/octet-stream") || !isTextual("application/json; charset=utf-8") || !isTextual("text/csv") {
		t.Fatal("isTextual misclassifies")
	}
}

func TestSubscribeResumesAndEnds(t *testing.T) {
	s, cap := newService(t)
	repo := container.Default.Repo
	sess := &db.ScanSession{Status: "scanned"}
	if err := repo.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateRun(sess.ID, db.RunPhaseTagging, "t")
	if err != nil {
		t.Fatal(err)
	}
	sid := strconv.FormatUint(uint64(sess.ID), 10)
	db.Emit(sess.ID, run.ID, db.EvProgress, db.ProgressData{RunID: run.ID, Phase: "tagging", Total: 3})

	sub, err := s.Subscribe(SubscribeRequest{SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Topic != "session://events/"+sid {
		t.Fatalf("topic = %q", sub.Topic)
	}
	cap.wait(t, 2) // run_status of CreateRun, then the progress event
	db.Emit(sess.ID, run.ID, db.EvSessionUpdated, struct{}{})
	cap.wait(t, 1)

	cap.mu.Lock()
	var seqs []int64
	for _, e := range cap.events {
		seqs = append(seqs, e.Seq)
	}
	first := cap.events[0]
	cap.mu.Unlock()
	if len(seqs) != 3 || seqs[0] >= seqs[1] || seqs[1] >= seqs[2] {
		t.Fatalf("seqs = %v", seqs)
	}
	if first.Kind != db.EvRunStatus || first.RunID != strconv.FormatUint(uint64(run.ID), 10) || !json.Valid([]byte(first.Data)) {
		t.Fatalf("first payload = %+v", first)
	}

	s.Unsubscribe(sub.SubscriptionID)
	cap.wait(t, 1)
	cap.mu.Lock()
	if len(cap.ends) != 1 || cap.ends[0].SubscriptionID != sub.SubscriptionID || cap.ends[0].Error != "" {
		t.Fatalf("ends = %+v", cap.ends)
	}
	cap.mu.Unlock()

	// resume after the first event replays exactly the later two
	after := seqs[0]
	cap2 := newCapture()
	s.emit = cap2.emit
	if _, err := s.Subscribe(SubscribeRequest{SessionID: sid, After: &after}); err != nil {
		t.Fatal(err)
	}
	cap2.wait(t, 2)
	cap2.mu.Lock()
	defer cap2.mu.Unlock()
	if len(cap2.events) != 2 || cap2.events[0].Seq != seqs[1] || cap2.events[1].Seq != seqs[2] {
		t.Fatalf("resumed = %+v, want seqs %v", cap2.events, seqs[1:])
	}
}

func TestSubscribeRejectsBadInput(t *testing.T) {
	s, _ := newService(t)
	for _, req := range []SubscribeRequest{{SessionID: "x"}, {SessionID: "0"}, {SessionID: "999"}} {
		if _, err := s.Subscribe(req); err == nil {
			t.Fatalf("Subscribe(%+v) must fail", req)
		}
	}
}

func TestShutdownEndsSubscriptions(t *testing.T) {
	s, cap := newService(t)
	sess := &db.ScanSession{Status: "scanned"}
	if err := container.Default.Repo.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subscribe(SubscribeRequest{SessionID: strconv.FormatUint(uint64(sess.ID), 10)}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := s.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("shutdown took %v", time.Since(start))
	}
	cap.wait(t, 1)
}

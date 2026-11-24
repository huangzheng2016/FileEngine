package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Workers 1.. are not created until worker 0's first response begins.
func TestWalkStartsOtherWorkersAfterWorkerZeroWarmsTheCache(t *testing.T) {
	a, _ := pipelineFixture(t, "")
	if err := a.prepass(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var events []string
	record := func(s string) {
		mu.Lock()
		events = append(events, s)
		mu.Unlock()
	}
	gate := newWarmGate()
	newWorker := func(id int) (*tagWorker, func(), error) {
		record("create")
		w := &tagWorker{id: id, logger: a.logger.ForWorker(id)}
		w.tb = NewToolBuilder(a.repo, nil, a.sessionID, 7, w.logger, a.cfg, a.session, false)
		if id == 0 {
			w.warm = gate
		}
		w.run = func(ctx context.Context, w *tagWorker, u workUnit) error {
			if id == 0 {
				time.Sleep(60 * time.Millisecond)
				record("response")
				gate.open()
			}
			_, err := w.tb.decideTool(ctx, &DecideInput{Items: []DecideItem{{Path: u.path, Action: actionContainer, Reason: "x"}}})
			return err
		}
		return w, func() {}, nil
	}
	if err := a.walk(context.Background(), newWorker); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) < 3 || events[0] != "create" || events[1] != "response" || events[2] != "create" {
		t.Fatalf("worker 0 starts alone, the rest after its response: %v", events)
	}
}

func TestWarmModelOpensOnFirstChunkOrFailure(t *testing.T) {
	g := newWarmGate()
	select {
	case <-g.ch:
		t.Fatal("closed at birth")
	default:
	}
	g.open()
	g.open()
	select {
	case <-g.ch:
	default:
		t.Fatal("open must close the channel exactly once")
	}
	old := warmTimeout
	warmTimeout = 20 * time.Millisecond
	defer func() { warmTimeout = old }()
	start := time.Now()
	newWarmGate().wait(context.Background())
	if time.Since(start) > time.Second {
		t.Fatal("a gate that never opens must time out")
	}
}

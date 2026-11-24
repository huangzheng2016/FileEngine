package settings

import (
	"context"
	"errors"
	"testing"

	"FileEngine/biz/dal/db/dbtest"
	"FileEngine/biz/errs"
	"FileEngine/internal/agent"
	"FileEngine/internal/config"
)

func newService(t *testing.T) *Service {
	t.Helper()
	return NewService(dbtest.New(t))
}

func setConcurrency(n int) func(*config.AgentConfig) {
	return func(a *config.AgentConfig) { a.Concurrency = n }
}

func TestSaveVersionsAndConflict(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	cur, _ := s.Current(ctx)
	if cur.Version != 0 || cur.Agent.Concurrency != 6 {
		t.Fatalf("initial: %+v", cur)
	}
	v1, err := s.Save(ctx, 0, setConcurrency(3))
	if err != nil || v1.Version != 1 {
		t.Fatalf("save: %+v %v", v1, err)
	}
	if got, _ := s.Current(ctx); got.Version != 1 || got.Agent.Concurrency != 3 {
		t.Fatalf("saved version must be current: %+v", got)
	}
	if _, err := s.Save(ctx, 0, setConcurrency(4)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale base: %v", err)
	}
	if got, _ := s.Current(ctx); got.Version != 1 || got.Agent.Concurrency != 3 {
		t.Fatalf("a rejected save must not change the settings: %+v", got)
	}
	same, err := s.Save(ctx, 1, setConcurrency(3))
	if err != nil || same.Version != 1 {
		t.Fatalf("unchanged save must keep the version: %+v %v", same, err)
	}
	v2, err := s.Save(ctx, 1, func(a *config.AgentConfig) { a.MaxRetries = 0; a.MaxStep = 20 })
	if err != nil || v2.Version != 2 || v2.Agent.MaxRetries != 0 {
		t.Fatalf("zero must be expressible: %+v %v", v2, err)
	}
	list, total, err := s.Versions(ctx, 0, 10)
	if err != nil || total != 2 || len(list) != 2 || list[0].Version != 2 {
		t.Fatalf("versions: %+v %d %v", list, total, err)
	}
	if got := list[0].Changed; len(got) != 2 || got[0] != "max_retries" || got[1] != "max_step" {
		t.Fatalf("changed = %v", got)
	}
	if got := list[1].Changed; len(got) != 1 || got[0] != "concurrency" {
		t.Fatalf("changed = %v", got)
	}
}

func TestSaveValidatesBeforeSwap(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	_, err := s.Save(ctx, 0, setConcurrency(1000))
	var e *errs.Error
	if !errors.As(err, &e) || e.Status != 400 {
		t.Fatalf("err = %v", err)
	}
	if cur, _ := s.Current(ctx); cur.Version != 0 {
		t.Fatalf("invalid save stored a version: %+v", cur)
	}
}

func TestPromptResetsToDefault(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	v1, err := s.SavePrompt(ctx, 0, "自定义")
	if err != nil {
		t.Fatal(err)
	}
	if p, custom := Prompt(v1.Agent); p != "自定义" || !custom {
		t.Fatalf("prompt = %q %v", p, custom)
	}
	v2, err := s.SavePrompt(ctx, 1, agent.DefaultSystemPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if _, custom := Prompt(v2.Agent); custom || v2.Agent.SystemPrompt != "" {
		t.Fatalf("default prompt must reset: %+v", v2.Agent.SystemPrompt)
	}
}

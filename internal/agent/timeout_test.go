package agent

import (
	"context"
	"testing"
	"time"

	"FileEngine/internal/config"
)

// agent_timeout semantics: <= 0 or unset means no timeout (no context
// deadline at all); a positive value caps one full agent run in seconds.
func TestAgentRunTimeoutSemantics(t *testing.T) {
	old := config.Get()
	defer func() {
		if old != nil {
			config.Update(old)
		}
	}()

	// Default (unset / zero): unlimited.
	config.Update(&config.Config{})
	if got := agentRunTimeout(); got != 0 {
		t.Fatalf("default agentRunTimeout: got %v, want 0 (unlimited)", got)
	}
	ctx, cancel := withRunTimeout(context.Background())
	defer cancel()
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		t.Fatal("unlimited run must not wrap ctx with a deadline")
	}

	// Negative: also unlimited.
	config.Update(&config.Config{Agent: config.AgentConfig{AgentTimeout: -5}})
	if got := agentRunTimeout(); got != 0 {
		t.Fatalf("negative agentRunTimeout: got %v, want 0 (unlimited)", got)
	}
	ctx, cancel2 := withRunTimeout(context.Background())
	defer cancel2()
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		t.Fatal("negative agent_timeout must not wrap ctx with a deadline")
	}

	// Positive: enforced per run.
	config.Update(&config.Config{Agent: config.AgentConfig{AgentTimeout: 45}})
	if got := agentRunTimeout(); got != 45*time.Second {
		t.Fatalf("agentRunTimeout: got %v, want 45s", got)
	}
	ctx, cancel3 := withRunTimeout(context.Background())
	defer cancel3()
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		t.Fatal("positive agent_timeout must wrap ctx with a deadline")
	}
	if until := time.Until(deadline); until < 40*time.Second || until > 45*time.Second {
		t.Fatalf("deadline: got %v from now, want ~45s", until)
	}
}

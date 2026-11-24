package agent

import (
	"context"
	"testing"
	"time"
)

func TestAgentRunTimeoutSemantics(t *testing.T) {
	if got := agentRunTimeout(0); got != 0 {
		t.Fatalf("default agentRunTimeout: got %v, want 0 (unlimited)", got)
	}
	ctx, cancel := withRunTimeout(context.Background(), 0)
	defer cancel()
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		t.Fatal("unlimited run must not wrap ctx with a deadline")
	}

	if got := agentRunTimeout(-5); got != 0 {
		t.Fatalf("negative agentRunTimeout: got %v, want 0 (unlimited)", got)
	}
	ctx, cancel2 := withRunTimeout(context.Background(), -5)
	defer cancel2()
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		t.Fatal("negative agent_timeout must not wrap ctx with a deadline")
	}

	if got := agentRunTimeout(45); got != 45*time.Second {
		t.Fatalf("agentRunTimeout: got %v, want 45s", got)
	}
	ctx, cancel3 := withRunTimeout(context.Background(), 45)
	defer cancel3()
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		t.Fatal("positive agent_timeout must wrap ctx with a deadline")
	}
	if until := time.Until(deadline); until < 40*time.Second || until > 45*time.Second {
		t.Fatalf("deadline: got %v from now, want ~45s", until)
	}
}

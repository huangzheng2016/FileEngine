package agent

import (
	"context"
	"testing"
)

// The stop→restart ABA race: run A is stopped, run B starts on the same
// Agent before A drains. A's deferred cleanup must NOT clear B's running
// flag — only the current run may finish itself.
func TestFinishRunDoesNotClobberNewerRun(t *testing.T) {
	a := &Agent{}
	cancel := context.CancelFunc(func() {})

	// Run A starts.
	a.mu.Lock()
	a.running = true
	a.cancelFn = cancel
	a.runGen = 1
	a.mu.Unlock()

	// Stop A, then run B starts before A drains (B bumps the generation).
	a.Stop()
	a.mu.Lock()
	a.running = true
	a.cancelFn = cancel
	a.runGen = 2
	a.mu.Unlock()

	// A finally drains — its cleanup must not clobber B.
	if a.finishRun(1) {
		t.Fatal("stale run A cleanup must be a no-op once B has started")
	}
	if !a.IsRunning() {
		t.Fatal("run B's running flag was clobbered by run A's cleanup")
	}

	// B's own cleanup still works.
	if !a.finishRun(2) {
		t.Fatal("current run B cleanup must apply")
	}
	if a.IsRunning() {
		t.Fatal("run B must report stopped after its own finishRun")
	}
}

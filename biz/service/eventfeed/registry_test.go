package eventfeed

import (
	"context"
	"testing"
	"time"
)

func TestRegistryCloseCancelsStreamsAndRefusesNewOnes(t *testing.T) {
	r := NewRegistry()
	ctx, done, err := r.Begin()
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() {
		<-ctx.Done()
		done()
		close(finished)
	}()

	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	<-finished
	if _, _, err := r.Begin(); err != ErrClosed {
		t.Fatalf("Begin after Close = %v", err)
	}
}

func TestRegistryCloseGivesUpAtDeadline(t *testing.T) {
	r := NewRegistry()
	_, done, err := r.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := r.Close(ctx); err == nil {
		t.Fatal("Close must report a stream that never finished")
	}
}

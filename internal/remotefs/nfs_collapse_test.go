package remotefs

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vmware/go-nfs-client/nfs/rpc"
)

func TestNFSReplenishRetriesUntilTheServerReturns(t *testing.T) {
	var calls atomic.Int32
	fresh := &fakeScanConn{}
	old := dialNFSScanConn
	dialNFSScanConn = func(string, string, rpc.Auth) (scanConn, error) {
		if calls.Add(1) < 3 {
			return nil, errors.New("connection refused")
		}
		return fresh, nil
	}
	t.Cleanup(func() { dialNFSScanConn = old })

	n := newPoolTestNFSFS(t)
	n.redialBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	n.pool = make(chan scanConn, 1)
	n.replenish()
	if got := n.poolSize.Load(); got != 1 || calls.Load() != 3 {
		t.Fatalf("poolSize=%d dials=%d, want 1 and 3", got, calls.Load())
	}
}

// A worker already waiting when the last pool connection dies must not wait
// for a connection that never comes. The primary connection is nil here, so
// reaching it panics; that panic is the observable "fell back" signal.
func TestNFSWaitingWorkerFallsBackWhenPoolCollapses(t *testing.T) {
	poison := &fakeScanConn{plusErr: os.ErrDeadlineExceeded}
	old := dialNFSScanConn
	dialed := make(chan struct{}, 1)
	dialNFSScanConn = func(string, string, rpc.Auth) (scanConn, error) {
		select {
		case dialed <- struct{}{}:
		default:
		}
		return nil, errors.New("connection refused")
	}
	t.Cleanup(func() { dialNFSScanConn = old })
	n := newPoolTestNFSFS(t)
	n.poolRecheck = 5 * time.Millisecond
	n.redialBackoff = []time.Duration{}
	n.pool = make(chan scanConn, 1)
	n.poolOnce.Do(func() {})
	n.pool <- poison
	n.poolSize.Store(1)

	// First worker takes the only connection, times out and discards it.
	first := make(chan error, 1)
	go func() {
		_, err := n.ListByHandle(context.Background(), ".", nil)
		first <- err
	}()
	if err := <-first; err == nil {
		t.Fatal("first listing should report the timeout")
	}
	<-dialed // the async re-dial has read the stub; safe to restore it later
	waitForCond(t, "pool collapse", func() bool { return n.poolSize.Load() == 0 })

	reached := make(chan bool, 1)
	go func() {
		defer func() { reached <- recover() != nil }()
		n.ListByHandle(context.Background(), ".", nil)
	}()
	select {
	case ok := <-reached:
		if !ok {
			t.Fatal("expected the fallback to hit the (nil) primary connection")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker still waiting on an empty pool")
	}
}

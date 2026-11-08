package remotefs

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	nfsclient "github.com/vmware/go-nfs-client/nfs"
	"github.com/vmware/go-nfs-client/nfs/rpc"
)

type fakeScanConn struct {
	entries      []*nfsclient.EntryPlus
	plusErr      error
	plusByFHLErr error
	closed       atomic.Bool
}

func (f *fakeScanConn) ReadDirPlus(string) ([]*nfsclient.EntryPlus, error) {
	return f.entries, f.plusErr
}

func (f *fakeScanConn) ReadDirPlusByHandle([]byte) ([]*nfsclient.EntryPlus, error) {
	return f.entries, f.plusByFHLErr
}

func (f *fakeScanConn) Close() { f.closed.Store(true) }

// An unexpected dial blocks and fails the test by timeout, which bounds
// runaway dialing.
func stubScanConnDialer(t *testing.T) (supply chan<- *fakeScanConn, calls *atomic.Int32) {
	t.Helper()
	sup := make(chan *fakeScanConn, 16)
	calls = &atomic.Int32{}
	old := dialNFSScanConn
	dialNFSScanConn = func(string, string, rpc.Auth) (scanConn, error) {
		calls.Add(1)
		return <-sup, nil
	}
	t.Cleanup(func() { dialNFSScanConn = old })
	return sup, calls
}

// FE_NFS_SCAN_CONNS=1 keeps the pool a single connection so discard and
// replenish are directly observable.
func newPoolTestNFSFS(t *testing.T) *NFSFS {
	t.Helper()
	t.Setenv("FE_NFS_SCAN_CONNS", "1")
	return &NFSFS{host: "fake", exportPath: "/", auth: rpc.AuthNull}
}

func waitForCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNFSPoolDiscardsTimedOutConn(t *testing.T) {
	supply, calls := stubScanConnDialer(t)
	poison := &fakeScanConn{plusErr: os.ErrDeadlineExceeded}
	good := &fakeScanConn{entries: []*nfsclient.EntryPlus{{FileName: "a.txt"}}}
	supply <- poison // initial ensurePool dial
	supply <- good   // replenish after the discard
	n := newPoolTestNFSFS(t)
	ctx := context.Background()

	_, err := n.ListByHandle(ctx, ".", nil)
	if err == nil {
		t.Fatal("ListByHandle on timing-out conn: got nil error")
	}
	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Protocol != "nfs" {
		t.Fatalf("error %v is not an nfs OpError", err)
	}
	if !IsTimeout(err) {
		t.Fatalf("error %v not recognized as timeout", err)
	}
	if !poison.closed.Load() {
		t.Fatal("timed-out connection was not closed (poison returned to pool?)")
	}

	waitForCond(t, "pool replenishment", func() bool {
		return calls.Load() == 2 && n.poolSize.Load() == 1
	})

	entries, err := n.ListByHandle(ctx, ".", nil)
	if err != nil {
		t.Fatalf("ListByHandle on replacement conn: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "a.txt" {
		t.Fatalf("got %v entries, want [a.txt]", entries)
	}
	if good.closed.Load() {
		t.Fatal("healthy connection was discarded")
	}
	if calls.Load() != 2 {
		t.Fatalf("dialer called %d times, want 2 (no extra dials)", calls.Load())
	}
}

func TestNFSPoolDiscardsConnWhenHandleTimeoutRecovers(t *testing.T) {
	supply, calls := stubScanConnDialer(t)
	flaky := &fakeScanConn{
		entries:      []*nfsclient.EntryPlus{{FileName: "b.txt"}},
		plusByFHLErr: os.ErrDeadlineExceeded,
	}
	supply <- flaky
	supply <- &fakeScanConn{} // replenish
	n := newPoolTestNFSFS(t)

	entries, err := n.ListByHandle(context.Background(), ".", []byte("handle"))
	if err != nil {
		t.Fatalf("fallback should have succeeded: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "b.txt" {
		t.Fatalf("got %v entries, want [b.txt]", entries)
	}
	if !flaky.closed.Load() {
		t.Fatal("connection with a timed-out handle attempt was not discarded")
	}
	waitForCond(t, "pool replenishment", func() bool {
		return calls.Load() == 2 && n.poolSize.Load() == 1
	})
}

func TestNFSPoolReturnsConnOnOrdinaryError(t *testing.T) {
	supply, calls := stubScanConnDialer(t)
	ordinary := &fakeScanConn{plusErr: errors.New("NFS3ERR_NOENT")}
	supply <- ordinary
	n := newPoolTestNFSFS(t)

	if _, err := n.ListByHandle(context.Background(), ".", nil); err == nil {
		t.Fatal("expected the ordinary error to surface")
	}
	if ordinary.closed.Load() {
		t.Fatal("connection discarded on a non-timeout error")
	}
	if calls.Load() != 1 {
		t.Fatalf("dialer called %d times, want 1 (no replenish)", calls.Load())
	}
	if got := n.poolSize.Load(); got != 1 {
		t.Fatalf("poolSize = %d, want 1", got)
	}
	if _, err := n.ListByHandle(context.Background(), ".", nil); err == nil {
		t.Fatal("expected the ordinary error to surface again")
	}
	if calls.Load() != 1 {
		t.Fatal("a new connection was dialed for a healthy returnee")
	}
}

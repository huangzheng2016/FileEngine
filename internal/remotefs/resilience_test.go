package remotefs

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// The timeout knob: unset env yields the 30s default, env overrides in
// seconds, and out-of-range values clamp to 1..600.
func TestOpTimeoutEnv(t *testing.T) {
	if got := opTimeout(); got != defaultOpTimeout {
		t.Fatalf("unset env: got %v, want default %v", got, defaultOpTimeout)
	}
	t.Setenv("FE_REMOTEFS_TIMEOUT_SEC", "5")
	if got := opTimeout(); got != 5*time.Second {
		t.Fatalf("env=5: got %v, want 5s", got)
	}
	t.Setenv("FE_REMOTEFS_TIMEOUT_SEC", "0")
	if got := opTimeout(); got != time.Second {
		t.Fatalf("env=0: got %v, want clamp to 1s", got)
	}
	t.Setenv("FE_REMOTEFS_TIMEOUT_SEC", "99999")
	if got := opTimeout(); got != 600*time.Second {
		t.Fatalf("env=99999: got %v, want clamp to 600s", got)
	}
	t.Setenv("FE_REMOTEFS_TIMEOUT_SEC", "junk")
	if got := opTimeout(); got != defaultOpTimeout {
		t.Fatalf("env=junk: got %v, want default %v", got, defaultOpTimeout)
	}
}

// OpError carries protocol/op/path context and unwraps cleanly.
func TestOpError(t *testing.T) {
	cause := os.ErrDeadlineExceeded
	err := &OpError{Protocol: "nfs", Op: "list", Path: "/photos/2024", Err: cause}

	msg := err.Error()
	for _, want := range []string{"nfs", "list", "/photos/2024", "i/o timeout"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("Error() = %q, want it to contain %q", msg, want)
		}
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("errors.Is through OpError lost the timeout sentinel")
	}
	if !IsTimeout(err) {
		t.Fatal("IsTimeout did not recognize a wrapped OS deadline")
	}
	if errors.Unwrap(err) != cause {
		t.Fatalf("Unwrap = %v, want the exact cause", cause)
	}

	dialErr := &OpError{Protocol: "smb", Op: "dial", Path: "nas.local", Err: cause}
	if got := dialErr.Error(); !strings.Contains(got, "smb dial") {
		t.Fatalf("dial Error() = %q", got)
	}
}

// IsTimeout recognizes every timeout flavor the backends can produce: OS
// deadlines (NFS/FTP/SFTP conn deadlines), context deadlines, and the legacy
// Timeout()-only shape (go-smb2's ContextError has no Unwrap).
func TestIsTimeout(t *testing.T) {
	legacy := &fakeLegacyTimeout{}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"os deadline", os.ErrDeadlineExceeded, true},
		{"ctx deadline", context.DeadlineExceeded, true},
		{"legacy Timeout()", legacy, true},
		{"wrapped legacy", &OpError{Protocol: "smb", Op: "list", Err: legacy}, true},
		{"plain", errors.New("boom"), false},
		{"wrapped plain", &OpError{Protocol: "ftp", Op: "stat", Err: errors.New("boom")}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := IsTimeout(tc.err); got != tc.want {
			t.Errorf("%s: IsTimeout = %v, want %v", tc.name, got, tc.want)
		}
		// The timeout sentinels must stay errors.Is-recognizable through
		// OpError wrapping for every timeout flavor.
		if tc.want {
			wrapped := &OpError{Protocol: "smb", Op: "list", Err: tc.err}
			if !errors.Is(wrapped, os.ErrDeadlineExceeded) {
				t.Errorf("%s: errors.Is(wrapped, os.ErrDeadlineExceeded) = false, want true", tc.name)
			}
		}
	}
}

// fakeLegacyTimeout signals a timeout only via the legacy Timeout() method,
// like go-smb2's ContextError (no Unwrap to a sentinel).
type fakeLegacyTimeout struct{}

func (e *fakeLegacyTimeout) Error() string { return "i/o timeout" }
func (e *fakeLegacyTimeout) Timeout() bool { return true }

// A Read on a stallConn with a silent peer must fail with a recognizable
// timeout instead of blocking forever — the core "dead connection" cure.
func TestStallConnReadTimeout(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close() // peer stays silent: never read from, never written to

	sc := &stallConn{Conn: c1, timeout: 100 * time.Millisecond}
	start := time.Now()
	_, err := sc.Read(make([]byte, 16))
	el := time.Since(start)
	if err == nil {
		t.Fatal("Read with silent peer: got nil error, want timeout")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read error %v not recognizable via errors.Is(os.ErrDeadlineExceeded)", err)
	}
	if el > 5*time.Second {
		t.Fatalf("Read took %v to fail; stall timeout was 100ms", el)
	}
}

// A Write on a stallConn whose peer never reads must hit the write deadline
// (net.Pipe is unbuffered, so the write blocks until the peer reads).
func TestStallConnWriteTimeout(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	sc := &stallConn{Conn: c1, timeout: 100 * time.Millisecond}
	start := time.Now()
	_, err := sc.Write([]byte("ping"))
	el := time.Since(start)
	if err == nil {
		t.Fatal("Write with non-reading peer: got nil error, want timeout")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Write error %v not recognizable via errors.Is(os.ErrDeadlineExceeded)", err)
	}
	if el > 5*time.Second {
		t.Fatalf("Write took %v to fail; stall timeout was 100ms", el)
	}
}

// A progressing transfer never trips the stall detector: bytes flow and the
// deadline stays irrelevant.
func TestStallConnProgress(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	sc := &stallConn{Conn: c1, timeout: time.Second}
	go func() {
		io.Copy(io.Discard, c2)
	}()
	for i := 0; i < 8; i++ {
		if _, err := sc.Write([]byte("chunk")); err != nil {
			t.Fatalf("Write chunk %d: %v", i, err)
		}
	}
}

// The SMB scan-concurrency knob follows the same shape as the NFS one:
// default 8, clamped to 1..32.
func TestSMBScanConnsEnv(t *testing.T) {
	if got := smbScanConns(); got != 8 {
		t.Fatalf("unset env: got %d, want 8", got)
	}
	t.Setenv("FE_SMB_SCAN_CONNS", "3")
	if got := smbScanConns(); got != 3 {
		t.Fatalf("env=3: got %d, want 3", got)
	}
	t.Setenv("FE_SMB_SCAN_CONNS", "0")
	if got := smbScanConns(); got != 1 {
		t.Fatalf("env=0: got %d, want clamp to 1", got)
	}
	t.Setenv("FE_SMB_SCAN_CONNS", "100")
	if got := smbScanConns(); got != 32 {
		t.Fatalf("env=100: got %d, want clamp to 32", got)
	}
}

// SFTP's deadline arming must set and cleanly clear the transport deadline:
// after disarm, a Read blocks again (no stale deadline fires later).
func TestSFTPArmDisarm(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	fs := &SFTPFS{conn: c1}
	disarm := fs.armDeadline(100 * time.Millisecond)
	_, err := c1.Read(make([]byte, 1))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("armed Read: got %v, want deadline error", err)
	}
	disarm()

	// Disarmed: a read unblocks when data arrives, however long it takes.
	go func() {
		time.Sleep(150 * time.Millisecond) // past the old deadline
		c2.Write([]byte("x"))
	}()
	if _, err := c1.Read(make([]byte, 1)); err != nil {
		t.Fatalf("disarmed Read: got %v, want nil", err)
	}
}

// opTimeout-scaled stream budget for SMB reads: 10x the per-op budget.
func TestSMBStreamTimeout(t *testing.T) {
	t.Setenv("FE_REMOTEFS_TIMEOUT_SEC", "10")
	if got := smbStreamTimeout(); got != 100*time.Second {
		t.Fatalf("got %v, want 100s (10x op timeout)", got)
	}
}

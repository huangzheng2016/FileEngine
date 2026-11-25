package remotefs

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/hirochachacha/go-smb2"
)

func TestSMBResolveRejectsBackslashAndStreams(t *testing.T) {
	s := &SMBFS{basePath: "/share/sub"}
	for _, p := range []string{`x\..\..\secret`, `a\b`, "file.txt:stream", "dir/a:b"} {
		if _, err := s.resolve(p); err == nil {
			t.Fatalf("resolve(%q): accepted", p)
		}
	}
	if _, err := s.resolve("ok/a b.txt"); err != nil {
		t.Fatal(err)
	}
}

type slowReader struct {
	chunks int
	every  time.Duration
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.chunks == 0 {
		return 0, io.EOF
	}
	time.Sleep(r.every)
	r.chunks--
	return copy(p, "x"), nil
}

func (r *slowReader) Close() error { return nil }

func (r *slowReader) ReadAt(p []byte, _ int64) (int, error) {
	time.Sleep(r.every)
	return copy(p, "x"), nil
}

func (r *slowReader) Seek(int64, int) (int64, error) { return 0, nil }

// stalledFile answers like go-smb2 when the server never replies: each call
// waits on the stream context and fails with its error.
type stalledFile struct{ ctx context.Context }

func (f stalledFile) wait() error {
	<-f.ctx.Done()
	return &os.PathError{Op: "read", Path: "p", Err: &smb2.ContextError{Err: f.ctx.Err()}}
}
func (f stalledFile) Read([]byte) (int, error)          { return 0, f.wait() }
func (f stalledFile) ReadAt([]byte, int64) (int, error) { return 0, f.wait() }
func (f stalledFile) Seek(int64, int) (int64, error)    { return 0, f.wait() }
func (f stalledFile) Close() error                      { return nil }

// A stalled stream fails as a timeout, which the scanner counts to detect a
// dead share; a cancelled one does not.
func TestSMBReadCloserStallIsTimeout(t *testing.T) {
	ctx, touch, cancel := slidingCtx(t.Context(), 50*time.Millisecond)
	defer cancel()
	rc := &smbReadCloser{f: stalledFile{ctx}, touch: touch, cancel: cancel, path: "p"}
	_, err := rc.ReadAt(make([]byte, 4), 100)
	if !IsTimeout(err) {
		t.Fatalf("stalled ReadAt: err = %v, want a timeout", err)
	}

	parent, stop := context.WithCancel(t.Context())
	ctx, touch, cancel = slidingCtx(parent, time.Minute)
	defer cancel()
	rc = &smbReadCloser{f: stalledFile{ctx}, touch: touch, cancel: cancel, path: "p"}
	stop()
	if _, err := rc.Read(make([]byte, 4)); err == nil || IsTimeout(err) {
		t.Fatalf("cancelled Read: err = %v, want a non-timeout error", err)
	}
}

// ReadAt is forwarded, so tail reads need no stream, and it slides the
// deadline like Read.
func TestSMBReadCloserReadAtSlides(t *testing.T) {
	timeout := 200 * time.Millisecond
	ctx, touch, cancel := slidingCtx(t.Context(), timeout)
	defer cancel()
	var rc io.ReadCloser = &smbReadCloser{f: &slowReader{every: 30 * time.Millisecond}, touch: touch, cancel: cancel, path: "p"}
	ra, ok := rc.(io.ReaderAt)
	if !ok {
		t.Fatal("smbReadCloser is not an io.ReaderAt")
	}
	start := time.Now()
	for time.Since(start) < 2*timeout {
		if _, err := ra.ReadAt(make([]byte, 1), 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	if ctx.Err() != nil {
		t.Fatal("context canceled while ReadAt was making progress")
	}
}

// A read stream longer than the idle timeout stays alive while data flows
// and is canceled once it stalls.
func TestSMBReadCloserSlidesWithProgress(t *testing.T) {
	timeout := 400 * time.Millisecond
	ctx, touch, cancel := slidingCtx(t.Context(), timeout)
	defer cancel()
	rc := &smbReadCloser{f: &slowReader{chunks: 20, every: 60 * time.Millisecond}, touch: touch, cancel: cancel, path: "p"}
	buf := make([]byte, 8)
	start := time.Now()
	for {
		if _, err := rc.Read(buf); err != nil {
			break
		}
	}
	if elapsed := time.Since(start); elapsed < timeout {
		t.Fatalf("stream finished in %v, test needs it to outlast the idle timeout", elapsed)
	}
	if ctx.Err() != nil {
		t.Fatal("context canceled while the stream was making progress")
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * timeout):
		t.Fatal("context never canceled after the stream stalled")
	}
}

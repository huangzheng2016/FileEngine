package remotefs

import (
	"io"
	"testing"
	"time"
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

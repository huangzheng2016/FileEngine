package remotefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"
)

// Write-operation contract shared by every backend:
//   - MoveFile renames in place and never replaces: an existing dst fails
//     with an error matching os.ErrExist. It never falls back to copying.
//   - CopyFile copies one regular file into a dst that must not exist yet
//     (os.ErrExist otherwise), returns the Close/commit error, compares the
//     bytes written with the source size and removes the partial dst on any
//     failure. Directories are rejected.
//   - Delete removes one file or one empty directory — never a tree.
//   - MkdirAll creates every missing level and succeeds when the directory
//     already exists.
// Parent directories of a MoveFile/CopyFile dst must already exist.

var ErrIsDir = errors.New("source is a directory")

func existErr(proto, op, p string) error {
	return &OpError{Protocol: proto, Op: op, Path: p, Err: os.ErrExist}
}

func shortCopyErr(proto, p string, written, want int64) error {
	return &OpError{Protocol: proto, Op: "copy", Path: p,
		Err: fmt.Errorf("short copy: wrote %d of %d bytes", written, want)}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// slidingCtx derives a context that is canceled once timeout passes without
// a touch: a per-operation deadline that slides with IO progress, for
// libraries that fix a request context for a file's whole lifetime (go-smb2).
func slidingCtx(parent context.Context, timeout time.Duration) (context.Context, func(), context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	touch := func() { last.Store(time.Now().UnixNano()) }
	go func() {
		tick := timeout / 4
		if tick <= 0 {
			tick = time.Millisecond
		}
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(0, last.Load())) > timeout {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, touch, cancel
}

type touchWriter struct {
	w     io.Writer
	touch func()
}

func (t *touchWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if n > 0 {
		t.touch()
	}
	return n, err
}

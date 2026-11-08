package remotefs

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Unwrap preserves the original error, so network timeouts stay recognizable
// via errors.Is(err, os.ErrDeadlineExceeded).
type OpError struct {
	Protocol string // "nfs", "smb", "sftp", "ftp", "local"
	Op       string // "dial", "mount", "list", "stat", "open", "read", ...
	Path     string // caller-facing path, or the host for dial operations
	Err      error
}

func (e *OpError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s %s: %v", e.Protocol, e.Op, e.Err)
	}
	return fmt.Sprintf("%s %s %q: %v", e.Protocol, e.Op, e.Path, e.Err)
}

func (e *OpError) Unwrap() error { return e.Err }

// Is makes the timeout sentinels match whenever the wrapped error is a
// timeout, including backends whose libraries signal a timeout only through
// the legacy Timeout() method (go-smb2's ContextError has no Unwrap, so the
// plain errors.Is traversal would stop short).
func (e *OpError) Is(target error) bool {
	if target != os.ErrDeadlineExceeded && target != context.DeadlineExceeded {
		return false
	}
	return IsTimeout(e.Err)
}

func IsTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

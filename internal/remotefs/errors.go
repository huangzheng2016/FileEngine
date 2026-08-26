package remotefs

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// OpError annotates a backend I/O failure with the protocol, the operation,
// and the path involved, so a surfaced error identifies the failing piece
// without extra breadcrumbs. Unwrap preserves the original error for
// errors.Is/errors.As — in particular network timeouts stay recognizable
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

// IsTimeout reports whether err is an operation timeout. It recognizes OS IO
// deadlines (net.Conn deadlines in the NFS/FTP/SFTP paths), context
// deadlines (per-request contexts in the SMB path), and errors that only
// implement the legacy Timeout() method — and works through OpError
// wrapping.
func IsTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

package remotefs

import (
	"net"
	"os"
	"strconv"
	"time"
)

// defaultOpTimeout bounds a single remote operation (dial, directory-list
// page, stat, open, read chunk) when FE_REMOTEFS_TIMEOUT_SEC is unset. It
// exists to cure the "dead connection, zero IO, goroutine blocked forever"
// failure mode observed in production (an NFS scan hung 18+ minutes on a
// silently dead TCP connection without surfacing any error): a peer that
// stops responding must fail the operation, not hang it.
const defaultOpTimeout = 30 * time.Second

// opTimeout reads FE_REMOTEFS_TIMEOUT_SEC (seconds, clamped to 1..600): the
// per-operation network timeout applied by every remote backend. Dialing
// uses the same budget.
func opTimeout() time.Duration {
	n := int(defaultOpTimeout / time.Second)
	if v := os.Getenv("FE_REMOTEFS_TIMEOUT_SEC"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			n = parsed
		}
	}
	if n < 1 {
		n = 1
	}
	if n > 600 {
		n = 600
	}
	return time.Duration(n) * time.Second
}

// stallConn wraps a net.Conn so every Read and Write syscall gets a fresh
// deadline: an IO stall longer than timeout surfaces an error (recognizable
// via errors.Is(err, os.ErrDeadlineExceeded)) instead of blocking forever.
// It is a stall detector, not an operation budget — a slow but progressing
// transfer never trips it.
//
// Only safe for connections with no background reader goroutine: a Read
// permanently blocked while the connection is merely idle would trip the
// deadline and kill a healthy session. Backends with a receive loop (SMB,
// SFTP/SSH) must arm deadlines around operations instead — see SFTPFS.
type stallConn struct {
	net.Conn
	timeout time.Duration
}

func (c *stallConn) Read(p []byte) (int, error) {
	if c.timeout > 0 {
		c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	}
	return c.Conn.Read(p)
}

func (c *stallConn) Write(p []byte) (int, error) {
	if c.timeout > 0 {
		c.Conn.SetWriteDeadline(time.Now().Add(c.timeout))
	}
	return c.Conn.Write(p)
}

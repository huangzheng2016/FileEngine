package remotefs

import (
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// A peer that stops responding must fail the operation, not hang it.
const defaultOpTimeout = 30 * time.Second

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

// activityConn is a gated, sliding IO deadline for connections with a
// permanently blocked background reader (ssh-style receive loops), where
// stallConn's always-armed deadlines would kill healthy idle sessions.
//
// arm/disarm bracket one operation (the caller serializes operations, so
// arm/disarm never races). While armed, every successful Read or Write
// re-arms the deadline, so the timeout fires only after a full interval
// without any IO progress — a long but progressing operation (a ReadDir
// paging through a giant directory) never trips it, while a silently dead
// peer still fails the operation instead of hanging it forever.
type activityConn struct {
	net.Conn
	timeout time.Duration
	mu      sync.Mutex
	armed   bool
}

func (c *activityConn) arm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed = true
	c.setDeadlines()
}

func (c *activityConn) disarm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed = false
	c.Conn.SetDeadline(time.Time{})
}

func (c *activityConn) setDeadlines() {
	deadline := time.Now().Add(c.timeout)
	c.Conn.SetReadDeadline(deadline)
	c.Conn.SetWriteDeadline(deadline)
}

func (c *activityConn) rearm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.armed {
		c.setDeadlines()
	}
}

func (c *activityConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.rearm()
	}
	return n, err
}

func (c *activityConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.rearm()
	}
	return n, err
}

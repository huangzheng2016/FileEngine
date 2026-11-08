package remotefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"sync"

	"FileEngine/internal/config"

	"github.com/jlaffaye/ftp"
)

// FTPFS serializes control-connection commands with mu: a *ftp.ServerConn is
// not safe for concurrent use, and eino executes parallel tool calls
// concurrently. Data transfers (the Retr response stream) run on their own
// TCP connection and stay parallel; only command/response framing is locked.
//
// A command that times out may leave the control stream desynced (a late
// reply would be misread as the next command's response), so the connection
// is dropped on timeout and lazily re-dialed by the next operation.
type FTPFS struct {
	mu       sync.Mutex
	conn     *ftp.ServerConn
	basePath string
	cfg      config.RemoteFSConfig // for re-dialing a dropped connection
	broken   bool                  // conn dropped as poisoned, re-dial before next use
}

func NewFTPFS(cfg config.RemoteFSConfig) (*FTPFS, error) {
	conn, err := dialFTP(cfg)
	if err != nil {
		return nil, err
	}
	return &FTPFS{conn: conn, basePath: cfg.BasePath, cfg: cfg}, nil
}

func dialFTP(cfg config.RemoteFSConfig) (*ftp.ServerConn, error) {
	port := cfg.Port
	if port == 0 {
		port = 21
	}

	timeout := opTimeout()
	// FTP has no background reader: control-connection replies are read
	// synchronously after each command and data connections only flow during
	// a transfer, so a per-syscall stall deadline is safe on both. The
	// library dials data connections through the same dial func.
	dial := func(network, address string) (net.Conn, error) {
		d := net.Dialer{Timeout: timeout}
		conn, err := d.Dial(network, address)
		if err != nil {
			return nil, err
		}
		return &stallConn{Conn: conn, timeout: timeout}, nil
	}
	conn, err := ftp.Dial(fmt.Sprintf("%s:%d", cfg.Host, port), ftp.DialWithDialFunc(dial))
	if err != nil {
		return nil, &OpError{Protocol: "ftp", Op: "dial", Path: cfg.Host, Err: err}
	}

	if cfg.Username != "" {
		if err := conn.Login(cfg.Username, cfg.Password); err != nil {
			conn.Quit()
			return nil, &OpError{Protocol: "ftp", Op: "login", Path: cfg.Host, Err: err}
		}
	}

	return conn, nil
}

// Callers must hold f.mu.
func (f *FTPFS) ensureConn() error {
	if !f.broken {
		return nil
	}
	conn, err := dialFTP(f.cfg)
	if err != nil {
		return &OpError{Protocol: "ftp", Op: "redial", Path: f.cfg.Host, Err: err}
	}
	f.conn = conn
	f.broken = false
	return nil
}

// dropConnLocked closes the current connection after a timeout: a stalled
// command may have left the control stream desynced, so the next operation
// re-dials instead of reusing it. Callers must hold f.mu (the transfer lock
// of an open ReadFile stream counts).
func (f *FTPFS) dropConnLocked() {
	f.conn.Quit() // best-effort; bounded by the stall deadline
	f.broken = true
}

func (f *FTPFS) resolve(p string) (string, error) {
	var full string
	if path.IsAbs(p) {
		full = path.Clean(p)
	} else {
		full = path.Join(f.basePath, p)
	}
	if err := confineSlashPath(full, f.basePath); err != nil {
		return "", err
	}
	return full, nil
}

func (f *FTPFS) List(_ context.Context, p string) ([]FileInfo, error) {
	fullPath, err := f.resolve(p)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.ensureConn(); err != nil {
		return nil, err
	}
	entries, err := f.conn.List(fullPath)
	if err != nil {
		if IsTimeout(err) {
			f.dropConnLocked()
		}
		return nil, &OpError{Protocol: "ftp", Op: "list", Path: p, Err: err}
	}
	var result []FileInfo
	for _, entry := range entries {
		if entry.Name == "." || entry.Name == ".." {
			continue
		}
		ft := FileTypeFile
		if entry.Type == ftp.EntryTypeFolder {
			ft = FileTypeDirectory
		} else if entry.Type == ftp.EntryTypeLink {
			ft = FileTypeSymlink
		}
		result = append(result, FileInfo{
			Name:     entry.Name,
			Path:     path.Join(p, entry.Name),
			Size:     int64(entry.Size),
			ModTime:  entry.Time,
			FileType: ft,
			IsDir:    entry.Type == ftp.EntryTypeFolder,
		})
	}
	return result, nil
}

func (f *FTPFS) Stat(_ context.Context, p string) (*FileInfo, error) {
	fullPath, err := f.resolve(p)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.ensureConn(); err != nil {
		return nil, err
	}
	entry, err := f.conn.GetEntry(fullPath)
	if err != nil {
		if IsTimeout(err) {
			f.dropConnLocked()
		}
		return nil, &OpError{Protocol: "ftp", Op: "stat", Path: p, Err: err}
	}
	ft := FileTypeFile
	if entry.Type == ftp.EntryTypeFolder {
		ft = FileTypeDirectory
	}
	return &FileInfo{
		Name:     entry.Name,
		Path:     p,
		Size:     int64(entry.Size),
		ModTime:  entry.Time,
		FileType: ft,
		IsDir:    entry.Type == ftp.EntryTypeFolder,
	}, nil
}

func (f *FTPFS) ReadFile(_ context.Context, p string) (io.ReadCloser, error) {
	fullPath, err := f.resolve(p)
	if err != nil {
		return nil, err
	}
	// Hold the control-connection lock for the whole transfer: jlaffaye/ftp
	// interleaves the 226 completion reply on the control connection, so any
	// other command issued between Retr and Close would read a stale reply
	// and desync the stream. Transfers serialize — same posture as NFS.
	f.mu.Lock()
	if err := f.ensureConn(); err != nil {
		f.mu.Unlock()
		return nil, err
	}
	resp, err := f.conn.Retr(fullPath)
	if err != nil {
		if IsTimeout(err) {
			f.dropConnLocked()
		}
		f.mu.Unlock()
		return nil, &OpError{Protocol: "ftp", Op: "open", Path: p, Err: err}
	}
	return &ftpLockedResponse{mu: &f.mu, fs: f, r: resp, path: p}, nil
}

// ftpLockedResponse holds the control-connection mutex from Retr until
// Close (once). Read itself needs no per-call locking — the lock is already
// held for the transfer's lifetime.
type ftpLockedResponse struct {
	mu   *sync.Mutex
	fs   *FTPFS
	r    *ftp.Response
	path string
	once sync.Once
}

func (l *ftpLockedResponse) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	if err != nil && err != io.EOF {
		if IsTimeout(err) {
			// The transfer lock is held, so dropping the (now desynced)
			// control connection here is safe.
			l.fs.dropConnLocked()
		}
		err = &OpError{Protocol: "ftp", Op: "read", Path: l.path, Err: err}
	}
	return n, err
}

func (l *ftpLockedResponse) Close() error {
	var err error
	l.once.Do(func() {
		err = l.r.Close()
		l.mu.Unlock()
	})
	return err
}

// MLST when the server supports it, otherwise a LIST of the parent. Caller
// holds f.mu with a live connection.
func (f *FTPFS) statLocked(full string) (*ftp.Entry, error) {
	if e, err := f.conn.GetEntry(full); err == nil {
		return e, nil
	} else if IsTimeout(err) {
		return nil, err
	}
	entries, err := f.conn.List(path.Dir(full))
	if err != nil {
		return nil, err
	}
	base := path.Base(full)
	for _, e := range entries {
		if e.Name == base {
			return e, nil
		}
	}
	return nil, os.ErrNotExist
}

func (f *FTPFS) MoveFile(_ context.Context, src, dst string) error {
	srcFull, err := f.resolve(src)
	if err != nil {
		return err
	}
	dstFull, err := f.resolve(dst)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.ensureConn(); err != nil {
		return err
	}
	// RNTO replaces an existing file on most servers; refuse instead.
	if _, err := f.statLocked(dstFull); err == nil {
		return existErr("ftp", "move", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		if IsTimeout(err) {
			f.dropConnLocked()
		}
		return &OpError{Protocol: "ftp", Op: "stat", Path: dst, Err: err}
	}
	if err := f.conn.Rename(srcFull, dstFull); err != nil {
		if IsTimeout(err) {
			f.dropConnLocked()
		}
		return &OpError{Protocol: "ftp", Op: "move", Path: src, Err: err}
	}
	return nil
}

// CopyFile streams the source over a dedicated control connection (RETR)
// straight into STOR on the primary one — one control connection cannot
// carry both transfers, and buffering the whole file in memory is not an
// option for large media.
func (f *FTPFS) CopyFile(_ context.Context, src, dst string) error {
	srcFull, err := f.resolve(src)
	if err != nil {
		return err
	}
	dstFull, err := f.resolve(dst)
	if err != nil {
		return err
	}
	srcConn, err := dialFTP(f.cfg)
	if err != nil {
		return err
	}
	defer srcConn.Quit()
	// SIZE fails on directories, which doubles as the directory check.
	size, err := srcConn.FileSize(srcFull)
	if err != nil {
		return &OpError{Protocol: "ftp", Op: "size", Path: src, Err: err}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.ensureConn(); err != nil {
		return err
	}
	if _, err := f.statLocked(dstFull); err == nil {
		return existErr("ftp", "create", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		if IsTimeout(err) {
			f.dropConnLocked()
		}
		return &OpError{Protocol: "ftp", Op: "stat", Path: dst, Err: err}
	}

	resp, err := srcConn.Retr(srcFull)
	if err != nil {
		return &OpError{Protocol: "ftp", Op: "open", Path: src, Err: err}
	}
	cr := &countingReader{r: resp}
	err = f.conn.Stor(dstFull, cr)
	if rerr := resp.Close(); err == nil {
		err = rerr
	}
	if err == nil && cr.n != size {
		err = shortCopyErr("ftp", dst, cr.n, size)
	}
	if err != nil {
		if IsTimeout(err) {
			f.dropConnLocked()
		} else {
			f.conn.Delete(dstFull)
		}
		if _, ok := err.(*OpError); !ok {
			err = &OpError{Protocol: "ftp", Op: "copy", Path: dst, Err: err}
		}
		return err
	}
	return nil
}

func (f *FTPFS) Delete(_ context.Context, p string) error {
	fullPath, err := f.resolve(p)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.ensureConn(); err != nil {
		return err
	}
	err = f.conn.Delete(fullPath)
	if err != nil && !IsTimeout(err) {
		err = f.conn.RemoveDir(fullPath)
	}
	if err != nil {
		if IsTimeout(err) {
			f.dropConnLocked()
		}
		return &OpError{Protocol: "ftp", Op: "delete", Path: p, Err: err}
	}
	return nil
}

func (f *FTPFS) MkdirAll(_ context.Context, p string) error {
	fullPath, err := f.resolve(p)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.ensureConn(); err != nil {
		return err
	}
	cur := ""
	if path.IsAbs(fullPath) {
		cur = "/"
	}
	for _, seg := range strings.Split(strings.Trim(fullPath, "/"), "/") {
		if seg == "" || seg == "." {
			continue
		}
		cur = path.Join(cur, seg)
		mkErr := f.conn.MakeDir(cur)
		if mkErr == nil {
			continue
		}
		if IsTimeout(mkErr) {
			f.dropConnLocked()
			return &OpError{Protocol: "ftp", Op: "mkdir", Path: p, Err: mkErr}
		}
		e, err := f.statLocked(cur)
		if err == nil && e.Type == ftp.EntryTypeFolder {
			continue
		}
		if err == nil {
			mkErr = fmt.Errorf("%s exists and is not a directory", cur)
		}
		return &OpError{Protocol: "ftp", Op: "mkdir", Path: p, Err: mkErr}
	}
	return nil
}

func (f *FTPFS) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn.Quit()
}

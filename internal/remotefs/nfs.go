package remotefs

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"FileEngine/internal/config"

	nfsclient "github.com/vmware/go-nfs-client/nfs"
	"github.com/vmware/go-nfs-client/nfs/rpc"
)

// NFSFS is safe for concurrent use: the locked go-nfs-client issues RPCs over
// one non-multiplexed TCP stream per connection (rpc.Client.Call has no
// locking — concurrent Calls interleave frames and fail with "xid did not
// match"), and eino executes parallel tool calls concurrently, so every RPC
// path here is serialized with mu.
//
// Scan listing is the exception: it runs over a pool of extra mount
// connections (FE_NFS_SCAN_CONNS, default 8), each checked out exclusively
// through a channel so a connection still sees only one RPC at a time. The
// primary connection above stays reserved for the agent/tool paths. The pool
// is dialed lazily on first scan use so agent workers (one NFSFS each) don't
// multiply connections.
type NFSFS struct {
	mu       sync.Mutex
	mount    *nfsclient.Mount
	target   *nfsclient.Target
	basePath string

	host       string
	auth       rpc.Auth
	exportPath string // resolved export, for dialing pool connections
	poolOnce   sync.Once
	pool       chan *nfsScanConn
	poolSize   int // successfully dialed pool connections (scan concurrency)
}

// nfsScanConn is one extra mount connection dedicated to scan-time directory
// listing. Exclusivity comes from the pool channel, not a mutex.
type nfsScanConn struct {
	mount  *nfsclient.Mount
	target *nfsclient.Target
}

// scanConnCount reads FE_NFS_SCAN_CONNS (default 8, clamped to 1..32): how
// many extra mount connections the scan pool dials for concurrent listing.
// Home NASes accept far more; the limit keeps Tailscale/NAT memory sane.
func scanConnCount() int {
	n := 8
	if v := os.Getenv("FE_NFS_SCAN_CONNS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			n = parsed
		}
	}
	if n < 1 {
		n = 1
	}
	if n > 32 {
		n = 32
	}
	return n
}

// nfsDialTimeoutOnce aligns the vendored RPC dial timeout with our
// per-operation budget exactly once (it is a package-level variable in the
// vendored library; repeated identical writes would still be a data race
// under -race when backends are constructed concurrently).
var nfsDialTimeoutOnce sync.Once

func NewNFSFS(cfg config.RemoteFSConfig) (*NFSFS, error) {
	port := cfg.Port
	if port == 0 {
		port = 2049
	}

	timeout := opTimeout()
	nfsDialTimeoutOnce.Do(func() { rpc.DialTimeout = timeout })

	mount, err := nfsclient.DialMount(cfg.Host)
	if err != nil {
		return nil, &OpError{Protocol: "nfs", Op: "dial", Path: cfg.Host, Err: err}
	}
	// Bound every RPC on the mount connection: without a timeout the
	// vendored transport reads with no deadline and a silently dead TCP
	// connection hangs the call forever (observed 18+ min in production).
	mount.SetTimeout(timeout)

	auth := rpc.NewAuthUnix("fileengine", 0, 0)

	// The export path is not necessarily the first path segment — servers may
	// export a deep path (e.g. fnOS exports "/fs/1000/nfs" whole). Probe the
	// MNT protocol from the deepest candidate upward; the first path that
	// mounts is the export, the remainder is the sub-path inside it.
	exportPath := strings.TrimSuffix(cfg.BasePath, "/")
	if exportPath == "" {
		exportPath = "/"
	}
	var target *nfsclient.Target
	var firstErr error
	for {
		target, err = mount.Mount(exportPath, auth.Auth())
		if err == nil {
			break
		}
		if firstErr == nil {
			firstErr = err
		}
		idx := strings.LastIndex(exportPath, "/")
		if idx <= 0 {
			mount.Close()
			return nil, &OpError{Protocol: "nfs", Op: "mount", Path: cfg.BasePath, Err: firstErr}
		}
		exportPath = exportPath[:idx]
	}
	// Mount dials a fresh connection for the NFS program: arm it too.
	target.SetTimeout(timeout)
	subPath := strings.TrimPrefix(strings.TrimPrefix(cfg.BasePath, exportPath), "/")

	return &NFSFS{
		mount:      mount,
		target:     target,
		basePath:   subPath,
		host:       cfg.Host,
		auth:       auth.Auth(),
		exportPath: exportPath,
	}, nil
}

// ensurePool lazily dials the scan-listing pool on first use. Failures
// degrade gracefully: fewer (or no) pool connections just mean less (or no)
// scan concurrency — the primary connection path is unaffected.
func (n *NFSFS) ensurePool() {
	n.poolOnce.Do(func() {
		want := scanConnCount()
		n.pool = make(chan *nfsScanConn, want)
		for i := 0; i < want; i++ {
			pm, err := nfsclient.DialMount(n.host)
			if err != nil {
				log.Printf("warn: nfs scan pool dial %d/%d: %v", i+1, want, err)
				break
			}
			pt, err := pm.Mount(n.exportPath, n.auth)
			if err != nil {
				log.Printf("warn: nfs scan pool mount %d/%d: %v", i+1, want, err)
				pm.Close()
				break
			}
			// Pool connections carry the same per-RPC timeout semantics as
			// the primary connection — a dead pooled connection must fail
			// its listing, not hang a scan worker forever.
			timeout := opTimeout()
			pm.SetTimeout(timeout)
			pt.SetTimeout(timeout)
			n.pool <- &nfsScanConn{mount: pm, target: pt}
			n.poolSize++
		}
	})
}

// resolve maps a caller path to an export-relative slash path confined to
// basePath (the sub-path inside the export; the server already confines us to
// the export itself). Absolute paths must stay inside basePath; ".." and
// absolute escapes are rejected.
func (n *NFSFS) resolve(p string) (string, error) {
	var full string
	if path.IsAbs(p) {
		full = path.Clean(p)
	} else {
		full = path.Join(n.basePath, p)
	}
	if err := confineSlashPath(full, n.basePath); err != nil {
		return "", err
	}
	return full, nil
}

func (n *NFSFS) List(_ context.Context, p string) ([]FileInfo, error) {
	fullPath, err := n.resolve(p)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	entries, err := n.target.ReadDirPlus(fullPath)
	if err != nil {
		return nil, &OpError{Protocol: "nfs", Op: "list", Path: p, Err: err}
	}
	return entriesToFileInfo(entries, p), nil
}

// ListConcurrency reports how many scan-listing calls may run in parallel —
// one per pooled connection. 1 means "no pool, list serially". The first
// call dials the pool (lazy), so call it once before sizing a worker pool.
func (n *NFSFS) ListConcurrency() int {
	n.ensurePool()
	if n.poolSize > 0 {
		return n.poolSize
	}
	return 1
}

// ListByHandle lists a directory for the scanner, on a pooled connection.
// When handle is non-nil (from a previous listing's FileInfo.Handle) the
// READDIRPLUS goes straight to the server-held file handle and skips the
// LOOKUP-per-path-component walk entirely; on any handle failure it falls
// back to the path-based walk. The path is still resolved through confine
// checks so lexical-confinement semantics match every other method. With no
// pool (pool dialing failed at startup) it degrades to the mutex-serialized
// primary connection.
func (n *NFSFS) ListByHandle(ctx context.Context, p string, handle []byte) ([]FileInfo, error) {
	fullPath, err := n.resolve(p)
	if err != nil {
		return nil, err
	}

	n.ensurePool()
	if n.poolSize > 0 {
		// Block until a pool connection is free — falling back to the
		// primary connection here would serialize the whole worker pool on
		// its mutex and starve the agent/tool paths.
		select {
		case c := <-n.pool:
			defer func() { n.pool <- c }()
			var entries []*nfsclient.EntryPlus
			if len(handle) > 0 {
				entries, err = c.target.ReadDirPlusByHandle(handle)
				if err != nil {
					// Stale or rejected handle: retry via the path walk
					// before giving up on the directory.
					entries, err = c.target.ReadDirPlus(fullPath)
				}
			} else {
				entries, err = c.target.ReadDirPlus(fullPath)
			}
			if err != nil {
				return nil, &OpError{Protocol: "nfs", Op: "list", Path: p, Err: err}
			}
			return entriesToFileInfo(entries, p), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// No pool: serial fallback on the primary connection, same locking as List.
	n.mu.Lock()
	defer n.mu.Unlock()
	entries, err := n.target.ReadDirPlus(fullPath)
	if err != nil {
		return nil, &OpError{Protocol: "nfs", Op: "list", Path: p, Err: err}
	}
	return entriesToFileInfo(entries, p), nil
}

// entriesToFileInfo maps READDIRPLUS entries to FileInfo, preserving the
// server-returned file handle so child listings can skip the LOOKUP walk.
func entriesToFileInfo(entries []*nfsclient.EntryPlus, p string) []FileInfo {
	var result []FileInfo
	for _, entry := range entries {
		if entry.FileName == "." || entry.FileName == ".." {
			continue
		}
		ft := FileTypeFile
		isDir := entry.IsDir()
		if isDir {
			ft = FileTypeDirectory
		} else if entry.Attr.Attr.Type == 5 { // NF3LNK
			ft = FileTypeSymlink
		}
		var handle []byte
		if entry.Handle.IsSet {
			handle = entry.Handle.FH
		}
		result = append(result, FileInfo{
			Name:        entry.FileName,
			Path:        path.Join(p, entry.FileName),
			Size:        entry.Size(),
			ModTime:     entry.ModTime(),
			Permissions: fmt.Sprintf("%04o", entry.Mode().Perm()),
			FileType:    ft,
			IsDir:       isDir,
			Handle:      handle,
		})
	}
	return result
}

func (n *NFSFS) Stat(_ context.Context, p string) (*FileInfo, error) {
	fullPath, err := n.resolve(p)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	info, _, err := n.target.Lookup(fullPath)
	if err != nil {
		return nil, &OpError{Protocol: "nfs", Op: "stat", Path: p, Err: err}
	}
	ft := FileTypeFile
	isDir := info.IsDir()
	if isDir {
		ft = FileTypeDirectory
	}
	name := path.Base(p)
	return &FileInfo{
		Name:        name,
		Path:        p,
		Size:        info.Size(),
		ModTime:     info.ModTime(),
		Permissions: fmt.Sprintf("%04o", info.Mode().Perm()),
		FileType:    ft,
		IsDir:       isDir,
	}, nil
}

func (n *NFSFS) ReadFile(_ context.Context, p string) (io.ReadCloser, error) {
	fullPath, err := n.resolve(p)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	f, err := n.target.Open(fullPath)
	n.mu.Unlock()
	if err != nil {
		return nil, &OpError{Protocol: "nfs", Op: "open", Path: p, Err: err}
	}
	return &nfsLockedFile{mu: &n.mu, f: f, path: p}, nil
}

// nfsLockedFile serializes an opened file's RPC-issuing methods (Read/Seek/
// Close) with the parent connection's mutex — the underlying rpc.Client is a
// single non-multiplexed stream, so concurrent reads must not interleave.
type nfsLockedFile struct {
	mu   *sync.Mutex
	f    *nfsclient.File
	path string
}

func (l *nfsLockedFile) Read(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n, err := l.f.Read(p)
	if err != nil && err != io.EOF {
		err = &OpError{Protocol: "nfs", Op: "read", Path: l.path, Err: err}
	}
	return n, err
}

func (l *nfsLockedFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Write(p)
}

func (l *nfsLockedFile) Seek(offset int64, whence int) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Seek(offset, whence)
}

func (l *nfsLockedFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

func (n *NFSFS) MoveFile(ctx context.Context, src, dst string) error {
	// NFS client doesn't have Rename; use copy + delete
	if err := n.CopyFile(ctx, src, dst); err != nil {
		return err
	}
	srcFull, err := n.resolve(src)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.target.Remove(srcFull)
}

func (n *NFSFS) CopyFile(_ context.Context, src, dst string) error {
	srcFull, err := n.resolve(src)
	if err != nil {
		return err
	}
	dstPath, err := n.resolve(dst)
	if err != nil {
		return err
	}
	// Hold the lock across the whole copy: the raw files below issue RPCs on
	// the same single-stream client without their own locking.
	n.mu.Lock()
	defer n.mu.Unlock()
	srcFile, err := n.target.Open(srcFull)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstDir := path.Dir(dstPath)
	n.target.Mkdir(dstDir, 0755)

	dstFile, err := n.target.OpenFile(dstPath, 0644)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (n *NFSFS) Delete(_ context.Context, p string) error {
	fullPath, err := n.resolve(p)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.target.RemoveAll(fullPath)
}

func (n *NFSFS) MkdirAll(_ context.Context, p string) error {
	fullPath, err := n.resolve(p)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	_, err = n.target.Mkdir(fullPath, 0755)
	return err
}

func (n *NFSFS) Close() error {
	// Drain and close the scan pool first. Callers close the fs only after
	// the scan (and its workers) have finished, so no connection is checked
	// out and no sender can race the close.
	if n.pool != nil {
		close(n.pool)
		for c := range n.pool {
			c.target.Close()
			c.mount.Close()
		}
		n.pool = nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.target.Close()
	return n.mount.Close()
}

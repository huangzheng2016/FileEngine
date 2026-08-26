package remotefs

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"FileEngine/internal/config"

	"github.com/hirochachacha/go-smb2"
)

type SMBFS struct {
	session  *smb2.Session
	share    *smb2.Share
	conn     net.Conn
	basePath string
}

// smbScanConns reads FE_SMB_SCAN_CONNS (default 8, clamped to 1..32): how
// many directory listings the scanner may run in parallel over the
// multiplexed SMB2 session. Named after the NFS knob to keep scan tuning
// symmetrical, though no connection pool is involved here.
func smbScanConns() int {
	n := 8
	if v := os.Getenv("FE_SMB_SCAN_CONNS"); v != "" {
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

// smbStreamTimeout bounds an opened file's whole read stream. go-smb2 fixes
// a file's request context at Open time, so per-Read re-arming is impossible
// without forking the library; a generous multiple of the per-operation
// budget keeps realistic reads unharmed while still surfacing a dead
// connection in minutes instead of never.
func smbStreamTimeout() time.Duration {
	return 10 * opTimeout()
}

func NewSMBFS(cfg config.RemoteFSConfig) (*SMBFS, error) {
	port := cfg.Port
	if port == 0 {
		port = 445
	}

	timeout := opTimeout()
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.Dial("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, &OpError{Protocol: "smb", Op: "dial", Path: cfg.Host, Err: err}
	}

	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     cfg.Username,
			Password: cfg.Password,
		},
	}

	// The negotiate/auth exchange waits on server replies with the dialer's
	// context; Background (the Dial default) would hang forever on a dead
	// peer, so bound it explicitly.
	dialCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	session, err := d.DialContext(dialCtx, conn)
	if err != nil {
		conn.Close()
		return nil, &OpError{Protocol: "smb", Op: "dial", Path: cfg.Host, Err: err}
	}

	// basePath format: "sharename/path/to/dir" (backslashes normalized to forward slashes)
	normalized := strings.ReplaceAll(cfg.BasePath, "\\", "/")
	normalized = strings.Trim(normalized, "/")
	shareName := normalized
	subPath := ""
	if idx := strings.Index(normalized, "/"); idx > 0 {
		shareName = normalized[:idx]
		subPath = normalized[idx:]
	}

	mountCtx, cancel := context.WithTimeout(context.Background(), timeout)
	share, err := session.WithContext(mountCtx).Mount(shareName)
	cancel()
	if err != nil {
		// Bound the cleanup too: the mount failure may be a dead peer.
		session.WithContext(dialCtx).Logoff()
		conn.Close()
		return nil, &OpError{Protocol: "smb", Op: "mount", Path: shareName, Err: err}
	}
	// Mount propagates the session's context into the share; re-base it, or
	// the canceled mount context would break every later operation (Umount
	// and the execute paths use the share's stored context directly).
	share = share.WithContext(context.Background())

	return &SMBFS{
		session:  session,
		share:    share,
		conn:     conn,
		basePath: subPath,
	}, nil
}

// resolve maps a caller path to a share-relative slash path confined to
// basePath (the sub-path inside the share; the server already confines us to
// the share itself). Absolute paths must stay inside basePath; ".." and
// absolute escapes are rejected.
func (s *SMBFS) resolve(p string) (string, error) {
	var joined string
	if path.IsAbs(p) {
		joined = path.Clean(p)
	} else {
		joined = path.Join(s.basePath, p)
	}
	if err := confineSlashPath(joined, s.basePath); err != nil {
		return "", err
	}
	resolved := strings.TrimPrefix(joined, "/")
	if resolved == "" {
		return ".", nil
	}
	return resolved, nil
}

// opCtx bounds one SMB operation. The go-smb2 transport arms no socket
// deadlines on its always-blocked receive loop; instead every request waits
// on the operation's context, so a timeout here is what keeps a dead peer
// from hanging the call forever. Each call goes through a fresh
// Share.WithContext copy, so concurrent operations never share a context.
func smbOpCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, opTimeout())
}

func (s *SMBFS) List(ctx context.Context, p string) ([]FileInfo, error) {
	fullPath, err := s.resolve(p)
	if err != nil {
		return nil, err
	}
	opCtx, cancel := smbOpCtx(ctx)
	defer cancel()
	entries, err := s.share.WithContext(opCtx).ReadDir(fullPath)
	if err != nil {
		return nil, &OpError{Protocol: "smb", Op: "list", Path: p, Err: err}
	}
	var result []FileInfo
	for _, info := range entries {
		ft := FileTypeFile
		if info.IsDir() {
			ft = FileTypeDirectory
		}
		result = append(result, FileInfo{
			Name:        info.Name(),
			Path:        path.Join(p, info.Name()),
			Size:        info.Size(),
			ModTime:     info.ModTime(),
			Permissions: fmt.Sprintf("%04o", info.Mode().Perm()),
			FileType:    ft,
			IsDir:       info.IsDir(),
		})
	}
	return result, nil
}

// ListConcurrency reports how many scan-listing calls may run in parallel.
// The go-smb2 connection multiplexes concurrent requests over the single
// TCP stream (per-message IDs, credits, locked response dispatch), so
// concurrent Share operations are safe without the connection pool the NFS
// backend needs; this only sizes the scanner's worker pool.
func (s *SMBFS) ListConcurrency() int {
	return smbScanConns()
}

// ListByHandle lists a directory for the scanner. SMB exposes no resumable
// directory handle to chain on, so handle is ignored and the listing always
// walks the path; the concurrency win comes from the multiplexed session,
// not from skipping path resolution.
func (s *SMBFS) ListByHandle(ctx context.Context, p string, _ []byte) ([]FileInfo, error) {
	return s.List(ctx, p)
}

func (s *SMBFS) Stat(ctx context.Context, p string) (*FileInfo, error) {
	fullPath, err := s.resolve(p)
	if err != nil {
		return nil, err
	}
	opCtx, cancel := smbOpCtx(ctx)
	defer cancel()
	info, err := s.share.WithContext(opCtx).Stat(fullPath)
	if err != nil {
		return nil, &OpError{Protocol: "smb", Op: "stat", Path: p, Err: err}
	}
	ft := FileTypeFile
	if info.IsDir() {
		ft = FileTypeDirectory
	}
	return &FileInfo{
		Name:        info.Name(),
		Path:        p,
		Size:        info.Size(),
		ModTime:     info.ModTime(),
		Permissions: fmt.Sprintf("%04o", info.Mode().Perm()),
		FileType:    ft,
		IsDir:       info.IsDir(),
	}, nil
}

func (s *SMBFS) ReadFile(ctx context.Context, p string) (io.ReadCloser, error) {
	fullPath, err := s.resolve(p)
	if err != nil {
		return nil, err
	}
	// The opened file keeps this context for its whole lifetime (see
	// smbStreamTimeout); cancel fires only when the returned closer runs.
	streamCtx, cancel := context.WithTimeout(ctx, smbStreamTimeout())
	f, err := s.share.WithContext(streamCtx).Open(fullPath)
	if err != nil {
		cancel()
		return nil, &OpError{Protocol: "smb", Op: "open", Path: p, Err: err}
	}
	return &smbReadCloser{f: f, cancel: cancel, path: p}, nil
}

// smbReadCloser pairs an opened SMB file with the cancel func of the stream
// context that bounds its reads.
type smbReadCloser struct {
	f      *smb2.File
	cancel context.CancelFunc
	path   string
}

func (r *smbReadCloser) Read(p []byte) (int, error) {
	n, err := r.f.Read(p)
	if err != nil && err != io.EOF {
		err = &OpError{Protocol: "smb", Op: "read", Path: r.path, Err: err}
	}
	return n, err
}

func (r *smbReadCloser) Close() error {
	err := r.f.Close()
	r.cancel()
	if err != nil {
		return &OpError{Protocol: "smb", Op: "close", Path: r.path, Err: err}
	}
	return nil
}

func (s *SMBFS) MoveFile(_ context.Context, src, dst string) error {
	srcFull, err := s.resolve(src)
	if err != nil {
		return err
	}
	dstFull, err := s.resolve(dst)
	if err != nil {
		return err
	}
	return s.share.Rename(srcFull, dstFull)
}

func (s *SMBFS) CopyFile(_ context.Context, src, dst string) error {
	srcFull, err := s.resolve(src)
	if err != nil {
		return err
	}
	dstFull, err := s.resolve(dst)
	if err != nil {
		return err
	}
	srcFile, err := s.share.Open(srcFull)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := s.share.Create(dstFull)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (s *SMBFS) Delete(_ context.Context, p string) error {
	fullPath, err := s.resolve(p)
	if err != nil {
		return err
	}
	return s.share.RemoveAll(fullPath)
}

func (s *SMBFS) MkdirAll(_ context.Context, p string) error {
	fullPath, err := s.resolve(p)
	if err != nil {
		return err
	}
	return s.share.MkdirAll(fullPath, 0755)
}

func (s *SMBFS) Close() error {
	// Umount and Logoff wait on server replies with the stored (Background)
	// context; bound them so Close cannot hang on a dead peer.
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout())
	defer cancel()
	s.share.WithContext(ctx).Umount()
	s.session.WithContext(ctx).Logoff()
	return s.conn.Close()
}

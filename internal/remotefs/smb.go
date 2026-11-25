package remotefs

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"FileEngine/internal/config"

	"github.com/hirochachacha/go-smb2"
)

type SMBFS struct {
	session  *smb2.Session
	share    *smb2.Share
	conn     net.Conn
	basePath string
}

// Named after the NFS knob for symmetric scan tuning, though no connection
// pool is involved.
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

func dialSMB(cfg config.RemoteFSConfig) (*smb2.Session, net.Conn, error) {
	port := cfg.Port
	if port == 0 {
		port = 445
	}

	timeout := opTimeout()
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.Dial("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, nil, &OpError{Protocol: "smb", Op: "dial", Path: cfg.Host, Err: err}
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
		return nil, nil, &OpError{Protocol: "smb", Op: "dial", Path: cfg.Host, Err: err}
	}
	return session, conn, nil
}

// ListSMBShares returns the disk shares a user can pick from; administrative
// and IPC shares (trailing $) are left out.
func ListSMBShares(ctx context.Context, cfg config.RemoteFSConfig) ([]string, error) {
	session, conn, err := dialSMB(cfg)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	opCtx, cancel := context.WithTimeout(ctx, opTimeout())
	defer cancel()
	s := session.WithContext(opCtx)
	defer s.Logoff()
	names, err := s.ListSharenames()
	if err != nil {
		return nil, &OpError{Protocol: "smb", Op: "shares", Path: cfg.Host, Err: err}
	}
	out := names[:0]
	for _, n := range names {
		if n != "" && !strings.HasSuffix(n, "$") {
			out = append(out, n)
		}
	}
	return out, nil
}

func NewSMBFS(cfg config.RemoteFSConfig) (*SMBFS, error) {
	timeout := opTimeout()
	session, conn, err := dialSMB(cfg)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

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
	// Defensive: go-smb2's Mount currently returns a share on a Background
	// context, so this only matters if that ever changes.
	share = share.WithContext(context.Background())

	return &SMBFS{
		session:  session,
		share:    share,
		conn:     conn,
		basePath: subPath,
	}, nil
}

// The server already confines us to the share; this confines to the sub-path
// inside it.
func (s *SMBFS) resolve(p string) (string, error) {
	// The server reads "\" as a separator, which the "/"-based confinement
	// below cannot see, and ":" opens an NTFS alternate data stream.
	if strings.ContainsAny(p, "\\:") {
		return "", fmt.Errorf("path %q contains a character SMB would interpret", p)
	}
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
	// A directory with hundreds of thousands of entries takes many
	// QUERY_DIRECTORY round trips; the deadline slides per page so only a
	// stalled listing times out.
	opCtx, touch, cancel := slidingCtx(ctx, opTimeout())
	defer cancel()
	dir, err := s.share.WithContext(opCtx).Open(fullPath)
	if err != nil {
		return nil, &OpError{Protocol: "smb", Op: "list", Path: p, Err: err}
	}
	defer dir.Close()
	touch()
	entries, err := readDirPaged(dir, touch)
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

const smbDirPage = 1024

type dirReader interface {
	Readdir(n int) ([]os.FileInfo, error)
}

// readDirPaged lists a whole directory, sorted by name like Share.ReadDir.
func readDirPaged(d dirReader, touch func()) ([]os.FileInfo, error) {
	var all []os.FileInfo
	for {
		page, err := d.Readdir(smbDirPage)
		all = append(all, page...)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		touch()
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name() < all[j].Name() })
	return all, nil
}

// go-smb2 multiplexes concurrent requests over one TCP stream, so no
// connection pool is needed; this only sizes the scanner's worker pool.
func (s *SMBFS) ListConcurrency() int {
	return smbScanConns()
}

// SMB exposes no resumable directory handle, so handle is ignored.
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
	// go-smb2 fixes an opened file's context for its lifetime, so the idle
	// timeout slides with every successful read instead of capping the whole
	// stream; cancel fires when the returned closer runs.
	streamCtx, touch, cancel := slidingCtx(ctx, opTimeout())
	f, err := s.share.WithContext(streamCtx).Open(fullPath)
	if err != nil {
		cancel()
		return nil, &OpError{Protocol: "smb", Op: "open", Path: p, Err: err}
	}
	touch()
	return &smbReadCloser{f: f, touch: touch, cancel: cancel, path: p}, nil
}

// smbFile is the part of *smb2.File a read stream uses.
type smbFile interface {
	io.ReadCloser
	io.ReaderAt
	io.Seeker
}

// Forwarding ReadAt and Seek lets callers read a file's tail (partial hashes,
// MP4 indexes at the end) without streaming the whole file.
type smbReadCloser struct {
	f      smbFile
	touch  func()
	cancel context.CancelFunc
	path   string
}

func (r *smbReadCloser) Read(p []byte) (int, error) {
	n, err := r.f.Read(p)
	if n > 0 {
		r.touch()
	}
	return n, r.wrap("read", err)
}

func (r *smbReadCloser) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.f.ReadAt(p, off)
	if n > 0 {
		r.touch()
	}
	return n, r.wrap("read", err)
}

func (r *smbReadCloser) Seek(offset int64, whence int) (int64, error) {
	ret, err := r.f.Seek(offset, whence)
	if err == nil {
		r.touch()
	}
	return ret, r.wrap("seek", err)
}

func (r *smbReadCloser) wrap(op string, err error) error {
	if err != nil && err != io.EOF {
		return &OpError{Protocol: "smb", Op: op, Path: r.path, Err: err}
	}
	return err
}

func (r *smbReadCloser) Close() error {
	err := r.f.Close()
	r.cancel()
	if err != nil {
		return &OpError{Protocol: "smb", Op: "close", Path: r.path, Err: err}
	}
	return nil
}

func (s *SMBFS) MoveFile(ctx context.Context, src, dst string) error {
	srcFull, err := s.resolve(src)
	if err != nil {
		return err
	}
	dstFull, err := s.resolve(dst)
	if err != nil {
		return err
	}
	opCtx, cancel := smbOpCtx(ctx)
	defer cancel()
	share := s.share.WithContext(opCtx)
	if _, err := share.Lstat(dstFull); err == nil {
		return existErr("smb", "move", dst)
	} else if !IsNotExist(err) {
		return &OpError{Protocol: "smb", Op: "stat", Path: dst, Err: err}
	}
	// go-smb2 renames with ReplaceIfExists=0: the server refuses an
	// existing target even if it appeared after the check above.
	if err := share.Rename(srcFull, dstFull); err != nil {
		return &OpError{Protocol: "smb", Op: "move", Path: src, Err: err}
	}
	return nil
}

// CopyFile runs on a sliding context: go-smb2 binds a file's context at open
// time, so the deadline is pushed forward by every successful write and only
// fires after opTimeout without progress (or when ctx is canceled).
func (s *SMBFS) CopyFile(ctx context.Context, src, dst string) error {
	srcFull, err := s.resolve(src)
	if err != nil {
		return err
	}
	dstFull, err := s.resolve(dst)
	if err != nil {
		return err
	}
	opCtx, touch, cancel := slidingCtx(ctx, opTimeout())
	defer cancel()
	share := s.share.WithContext(opCtx)

	srcFile, err := share.Open(srcFull)
	if err != nil {
		return &OpError{Protocol: "smb", Op: "open", Path: src, Err: err}
	}
	defer srcFile.Close()
	st, err := srcFile.Stat()
	if err != nil {
		return &OpError{Protocol: "smb", Op: "stat", Path: src, Err: err}
	}
	if st.IsDir() {
		return &OpError{Protocol: "smb", Op: "copy", Path: src, Err: ErrIsDir}
	}

	// O_EXCL maps to FILE_CREATE: the server refuses an existing dst.
	dstFile, err := share.OpenFile(dstFull, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if _, serr := share.Lstat(dstFull); serr == nil {
			return existErr("smb", "create", dst)
		}
		return &OpError{Protocol: "smb", Op: "create", Path: dst, Err: err}
	}
	var n int64
	err = noticeCreated(ctx)
	if err == nil {
		n, err = io.Copy(&touchWriter{w: dstFile, touch: touch}, srcFile)
	}
	if cerr := dstFile.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != st.Size() {
		err = shortCopyErr("smb", dst, n, st.Size())
	}
	if err != nil {
		// The sliding context may be the thing that failed; clean up on a
		// fresh bounded one.
		rmCtx, rmCancel := smbOpCtx(context.Background())
		s.share.WithContext(rmCtx).Remove(dstFull)
		rmCancel()
		if _, ok := err.(*OpError); !ok {
			err = &OpError{Protocol: "smb", Op: "copy", Path: dst, Err: err}
		}
		return err
	}
	touch()
	_ = share.Chtimes(dstFull, st.ModTime(), st.ModTime())
	return nil
}

func (s *SMBFS) Delete(ctx context.Context, p string) error {
	fullPath, err := s.resolve(p)
	if err != nil {
		return err
	}
	opCtx, cancel := smbOpCtx(ctx)
	defer cancel()
	if err := s.share.WithContext(opCtx).Remove(fullPath); err != nil {
		return &OpError{Protocol: "smb", Op: "delete", Path: p, Err: err}
	}
	return nil
}

func (s *SMBFS) MkdirAll(ctx context.Context, p string) error {
	fullPath, err := s.resolve(p)
	if err != nil {
		return err
	}
	opCtx, cancel := smbOpCtx(ctx)
	defer cancel()
	if err := s.share.WithContext(opCtx).MkdirAll(fullPath, 0755); err != nil {
		return &OpError{Protocol: "smb", Op: "mkdir", Path: p, Err: err}
	}
	return nil
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

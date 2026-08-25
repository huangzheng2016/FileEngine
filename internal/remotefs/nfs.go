package remotefs

import (
	"context"
	"fmt"
	"io"
	"path"
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
type NFSFS struct {
	mu       sync.Mutex
	mount    *nfsclient.Mount
	target   *nfsclient.Target
	basePath string
}

func NewNFSFS(cfg config.RemoteFSConfig) (*NFSFS, error) {
	port := cfg.Port
	if port == 0 {
		port = 2049
	}

	mount, err := nfsclient.DialMount(cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("nfs dial mount: %w", err)
	}

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
			return nil, fmt.Errorf("nfs mount %s: %w", cfg.BasePath, firstErr)
		}
		exportPath = exportPath[:idx]
	}
	subPath := strings.TrimPrefix(strings.TrimPrefix(cfg.BasePath, exportPath), "/")

	return &NFSFS{
		mount:    mount,
		target:   target,
		basePath: subPath,
	}, nil
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
		return nil, err
	}
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
		result = append(result, FileInfo{
			Name:        entry.FileName,
			Path:        path.Join(p, entry.FileName),
			Size:        entry.Size(),
			ModTime:     entry.ModTime(),
			Permissions: fmt.Sprintf("%04o", entry.Mode().Perm()),
			FileType:    ft,
			IsDir:       isDir,
		})
	}
	return result, nil
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
		return nil, err
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
		return nil, err
	}
	return &nfsLockedFile{mu: &n.mu, f: f}, nil
}

// nfsLockedFile serializes an opened file's RPC-issuing methods (Read/Seek/
// Close) with the parent connection's mutex — the underlying rpc.Client is a
// single non-multiplexed stream, so concurrent reads must not interleave.
type nfsLockedFile struct {
	mu *sync.Mutex
	f  *nfsclient.File
}

func (l *nfsLockedFile) Read(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Read(p)
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
	n.mu.Lock()
	defer n.mu.Unlock()
	n.target.Close()
	return n.mount.Close()
}

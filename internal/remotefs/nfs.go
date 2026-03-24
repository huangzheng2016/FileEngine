package remotefs

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"FileEngine/internal/config"

	nfsclient "github.com/vmware/go-nfs-client/nfs"
	"github.com/vmware/go-nfs-client/nfs/rpc"
)

type NFSFS struct {
	mount    *nfsclient.Mount
	target   *nfsclient.Target
	basePath string
}

func NewNFSFS(cfg config.RemoteFSConfig) (*NFSFS, error) {
	port := cfg.Port
	if port == 0 {
		port = 2049
	}

	// basePath format: "export_path" or "export_path/sub/dir"
	exportPath := cfg.BasePath
	subPath := ""
	if idx := strings.Index(cfg.BasePath[1:], "/"); idx > 0 {
		exportPath = cfg.BasePath[:idx+1]
		subPath = cfg.BasePath[idx+1:]
	}

	mount, err := nfsclient.DialMount(cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("nfs dial mount: %w", err)
	}

	auth := rpc.NewAuthUnix("fileengine", 0, 0)
	target, err := mount.Mount(exportPath, auth.Auth())
	if err != nil {
		mount.Close()
		return nil, fmt.Errorf("nfs mount %s: %w", exportPath, err)
	}

	return &NFSFS{
		mount:    mount,
		target:   target,
		basePath: subPath,
	}, nil
}

func (n *NFSFS) resolve(p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(n.basePath, p)
}

func (n *NFSFS) List(_ context.Context, p string) ([]FileInfo, error) {
	entries, err := n.target.ReadDirPlus(n.resolve(p))
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
	fullPath := n.resolve(p)
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
	f, err := n.target.Open(n.resolve(p))
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (n *NFSFS) MoveFile(ctx context.Context, src, dst string) error {
	// NFS client doesn't have Rename; use copy + delete
	if err := n.CopyFile(ctx, src, dst); err != nil {
		return err
	}
	return n.target.Remove(n.resolve(src))
}

func (n *NFSFS) CopyFile(_ context.Context, src, dst string) error {
	srcFile, err := n.target.Open(n.resolve(src))
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstPath := n.resolve(dst)
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

func (n *NFSFS) MkdirAll(_ context.Context, p string) error {
	_, err := n.target.Mkdir(n.resolve(p), 0755)
	return err
}

func (n *NFSFS) Close() error {
	n.target.Close()
	return n.mount.Close()
}

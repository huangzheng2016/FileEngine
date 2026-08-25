package remotefs

import (
	"context"
	"fmt"
	"io"
	"net"
	"path"
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

func NewSMBFS(cfg config.RemoteFSConfig) (*SMBFS, error) {
	port := cfg.Port
	if port == 0 {
		port = 445
	}

	conn, err := net.Dial("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("smb connect: %w", err)
	}

	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     cfg.Username,
			Password: cfg.Password,
		},
	}

	session, err := d.Dial(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smb dial: %w", err)
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

	share, err := session.Mount(shareName)
	if err != nil {
		session.Logoff()
		conn.Close()
		return nil, fmt.Errorf("smb mount %s: %w", shareName, err)
	}

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

func (s *SMBFS) List(_ context.Context, p string) ([]FileInfo, error) {
	fullPath, err := s.resolve(p)
	if err != nil {
		return nil, err
	}
	entries, err := s.share.ReadDir(fullPath)
	if err != nil {
		return nil, err
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

func (s *SMBFS) Stat(_ context.Context, p string) (*FileInfo, error) {
	fullPath, err := s.resolve(p)
	if err != nil {
		return nil, err
	}
	info, err := s.share.Stat(fullPath)
	if err != nil {
		return nil, err
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

func (s *SMBFS) ReadFile(_ context.Context, p string) (io.ReadCloser, error) {
	fullPath, err := s.resolve(p)
	if err != nil {
		return nil, err
	}
	return s.share.Open(fullPath)
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
	s.share.Umount()
	s.session.Logoff()
	return s.conn.Close()
}

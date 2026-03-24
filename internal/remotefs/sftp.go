package remotefs

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"

	"FileEngine/internal/config"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SFTPFS struct {
	client   *sftp.Client
	sshConn  *ssh.Client
	basePath string
}

func NewSFTPFS(cfg config.RemoteFSConfig) (*SFTPFS, error) {
	var authMethods []ssh.AuthMethod
	if cfg.KeyPath != "" {
		key, err := os.ReadFile(cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read key file: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		authMethods = append(authMethods, ssh.Password(cfg.Password))
	}

	port := cfg.Port
	if port == 0 {
		port = 22
	}

	sshClient, err := ssh.Dial("tcp", net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", port)), &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}

	client, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("sftp client: %w", err)
	}

	return &SFTPFS{
		client:   client,
		sshConn:  sshClient,
		basePath: cfg.BasePath,
	}, nil
}

func (s *SFTPFS) resolve(p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(s.basePath, p)
}

func (s *SFTPFS) List(_ context.Context, p string) ([]FileInfo, error) {
	fullPath := s.resolve(p)
	entries, err := s.client.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}
	var result []FileInfo
	for _, info := range entries {
		ft := FileTypeFile
		if info.IsDir() {
			ft = FileTypeDirectory
		} else if info.Mode()&os.ModeSymlink != 0 {
			ft = FileTypeSymlink
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

func (s *SFTPFS) Stat(_ context.Context, p string) (*FileInfo, error) {
	fullPath := s.resolve(p)
	info, err := s.client.Stat(fullPath)
	if err != nil {
		return nil, err
	}
	ft := FileTypeFile
	if info.IsDir() {
		ft = FileTypeDirectory
	}
	name := info.Name()
	if name == "" || name == "/" {
		parts := strings.Split(strings.TrimRight(p, "/"), "/")
		if len(parts) > 0 {
			name = parts[len(parts)-1]
		}
	}
	return &FileInfo{
		Name:        name,
		Path:        p,
		Size:        info.Size(),
		ModTime:     info.ModTime(),
		Permissions: fmt.Sprintf("%04o", info.Mode().Perm()),
		FileType:    ft,
		IsDir:       info.IsDir(),
	}, nil
}

func (s *SFTPFS) ReadFile(_ context.Context, p string) (io.ReadCloser, error) {
	return s.client.Open(s.resolve(p))
}

func (s *SFTPFS) MoveFile(ctx context.Context, src, dst string) error {
	dstFull := s.resolve(dst)
	if err := s.client.MkdirAll(path.Dir(dstFull)); err != nil {
		return err
	}
	return s.client.Rename(s.resolve(src), dstFull)
}

func (s *SFTPFS) CopyFile(_ context.Context, src, dst string) error {
	srcFull := s.resolve(src)
	dstFull := s.resolve(dst)

	if err := s.client.MkdirAll(path.Dir(dstFull)); err != nil {
		return err
	}

	srcFile, err := s.client.Open(srcFull)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := s.client.Create(dstFull)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (s *SFTPFS) MkdirAll(_ context.Context, p string) error {
	return s.client.MkdirAll(s.resolve(p))
}

func (s *SFTPFS) Close() error {
	s.client.Close()
	return s.sshConn.Close()
}

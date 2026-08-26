package remotefs

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"FileEngine/internal/config"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SFTPFS serializes its read operations (List/Stat/ReadFile/Close) with mu.
// pkg/sftp itself is safe for concurrent use, but bounding an operation
// requires arming an IO deadline on the shared ssh transport, and concurrent
// arm/disarm on one connection is incoherent without serialization (the ssh
// read loop is always blocked in Read, so a deadline left armed would kill a
// healthy idle session). The posture matches the NFS and FTP backends: one
// operation at a time, each bounded by opTimeout.
type SFTPFS struct {
	mu       sync.Mutex
	client   *sftp.Client
	sshConn  *ssh.Client
	conn     net.Conn // raw transport, for deadline arming
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

	timeout := opTimeout()
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, &OpError{Protocol: "sftp", Op: "dial", Path: cfg.Host, Err: err}
	}

	// The ssh handshake and the sftp INIT exchange block on server replies;
	// keep an absolute deadline armed until the client is fully up. Steady
	// state runs with no deadline — arming happens per operation instead.
	conn.SetDeadline(time.Now().Add(timeout))
	sshTransport, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		conn.Close()
		return nil, &OpError{Protocol: "sftp", Op: "dial", Path: cfg.Host, Err: err}
	}

	sshClient := ssh.NewClient(sshTransport, chans, reqs)
	client, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, &OpError{Protocol: "sftp", Op: "dial", Path: cfg.Host, Err: err}
	}
	conn.SetDeadline(time.Time{})

	return &SFTPFS{
		client:   client,
		sshConn:  sshClient,
		conn:     conn,
		basePath: cfg.BasePath,
	}, nil
}

// armDeadline sets an absolute IO deadline on the ssh transport and returns
// the disarm func. Callers must hold s.mu so concurrent operations cannot
// race arm/disarm.
func (s *SFTPFS) armDeadline(d time.Duration) (disarm func()) {
	s.conn.SetDeadline(time.Now().Add(d))
	return func() { s.conn.SetDeadline(time.Time{}) }
}

// resolve maps a caller path to a host slash path and confines it to
// basePath: absolute paths are accepted only inside basePath, and ".." or
// absolute escapes are rejected (matches LocalFS.resolve's lexical check).
func (s *SFTPFS) resolve(p string) (string, error) {
	var full string
	if path.IsAbs(p) {
		full = path.Clean(p)
	} else {
		full = path.Join(s.basePath, p)
	}
	if err := confineSlashPath(full, s.basePath); err != nil {
		return "", err
	}
	return full, nil
}

func (s *SFTPFS) List(_ context.Context, p string) ([]FileInfo, error) {
	fullPath, err := s.resolve(p)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	disarm := s.armDeadline(opTimeout())
	defer disarm()
	entries, err := s.client.ReadDir(fullPath)
	if err != nil {
		return nil, &OpError{Protocol: "sftp", Op: "list", Path: p, Err: err}
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
	fullPath, err := s.resolve(p)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	disarm := s.armDeadline(opTimeout())
	defer disarm()
	info, err := s.client.Stat(fullPath)
	if err != nil {
		return nil, &OpError{Protocol: "sftp", Op: "stat", Path: p, Err: err}
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
	fullPath, err := s.resolve(p)
	if err != nil {
		return nil, err
	}
	// The mutex stays held for the stream's lifetime (same posture as the
	// NFS/FTP locked files): deadline arming on the shared transport is only
	// coherent while no other operation runs.
	s.mu.Lock()
	disarm := s.armDeadline(opTimeout())
	f, err := s.client.Open(fullPath)
	disarm()
	if err != nil {
		s.mu.Unlock()
		return nil, &OpError{Protocol: "sftp", Op: "open", Path: p, Err: err}
	}
	return &sftpLockedFile{mu: &s.mu, conn: s.conn, timeout: opTimeout(), f: f, path: p}, nil
}

// sftpLockedFile holds the parent connection's mutex from Open until Close;
// each Read/Close arms a fresh stall deadline on the shared transport.
type sftpLockedFile struct {
	mu      *sync.Mutex
	conn    net.Conn
	timeout time.Duration
	f       *sftp.File
	path    string
	once    sync.Once
}

func (l *sftpLockedFile) Read(p []byte) (int, error) {
	l.conn.SetDeadline(time.Now().Add(l.timeout))
	n, err := l.f.Read(p)
	l.conn.SetDeadline(time.Time{})
	if err != nil && err != io.EOF {
		err = &OpError{Protocol: "sftp", Op: "read", Path: l.path, Err: err}
	}
	return n, err
}

func (l *sftpLockedFile) Close() error {
	var err error
	l.once.Do(func() {
		l.conn.SetDeadline(time.Now().Add(l.timeout))
		err = l.f.Close()
		l.conn.SetDeadline(time.Time{})
		l.mu.Unlock()
	})
	return err
}

func (s *SFTPFS) MoveFile(ctx context.Context, src, dst string) error {
	dstFull, err := s.resolve(dst)
	if err != nil {
		return err
	}
	srcFull, err := s.resolve(src)
	if err != nil {
		return err
	}
	if err := s.client.MkdirAll(path.Dir(dstFull)); err != nil {
		return err
	}
	return s.client.Rename(srcFull, dstFull)
}

func (s *SFTPFS) CopyFile(_ context.Context, src, dst string) error {
	srcFull, err := s.resolve(src)
	if err != nil {
		return err
	}
	dstFull, err := s.resolve(dst)
	if err != nil {
		return err
	}

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

func (s *SFTPFS) Delete(_ context.Context, p string) error {
	fullPath, err := s.resolve(p)
	if err != nil {
		return err
	}
	return s.client.RemoveAll(fullPath)
}

func (s *SFTPFS) MkdirAll(_ context.Context, p string) error {
	fullPath, err := s.resolve(p)
	if err != nil {
		return err
	}
	return s.client.MkdirAll(fullPath)
}

func (s *SFTPFS) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client.Close()
	return s.sshConn.Close()
}

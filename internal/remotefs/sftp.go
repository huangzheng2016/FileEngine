package remotefs

import (
	"context"
	"crypto/subtle"
	"errors"
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

// SFTPFS serializes every operation (reads, writes, Close) with mu.
// pkg/sftp itself is safe for concurrent use, but bounding an operation
// requires arming an IO deadline on the shared ssh transport, and concurrent
// arm/disarm on one connection is incoherent without serialization (the ssh
// read loop is always blocked in Read, so a deadline left armed would kill a
// healthy idle session). The posture matches the NFS and FTP backends: one
// operation at a time. The deadline is sliding — see activityConn: it fires
// only after opTimeout without any IO progress, so a long but progressing
// operation (a ReadDir paging through a giant directory) is never cut off.
type SFTPFS struct {
	mu       sync.Mutex
	client   *sftp.Client
	sshConn  *ssh.Client
	conn     *activityConn
	basePath string
}

var ErrHostKeyNotPinned = errors.New("未固定 SFTP 服务器指纹：请先测试连接并保存")

// A possible man-in-the-middle, or a reinstalled server: the user must review
// and re-pin explicitly.
type HostKeyMismatchError struct {
	Expected, Got string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("服务器指纹已变化（已固定 %s，当前 %s）：请确认服务器身份后重新固定指纹", e.Expected, e.Got)
}

func pinnedHostKey(expected string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		got := ssh.FingerprintSHA256(key)
		if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			return &HostKeyMismatchError{Expected: expected, Got: got}
		}
		return nil
	}
}

var errHostKeyCaptured = errors.New("host key captured")

func FetchSSHHostKey(host string, port int) (string, error) {
	if port == 0 {
		port = 22
	}
	timeout := opTimeout()
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return "", &OpError{Protocol: "sftp", Op: "dial", Path: host, Err: err}
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	var fp string
	_, _, _, err = ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp = ssh.FingerprintSHA256(key)
			return errHostKeyCaptured
		},
	})
	if fp != "" {
		return fp, nil
	}
	if err == nil {
		err = errors.New("server presented no host key")
	}
	return "", &OpError{Protocol: "sftp", Op: "hostkey", Path: host, Err: err}
}

func NewSFTPFS(cfg config.RemoteFSConfig) (*SFTPFS, error) {
	if cfg.HostKey == "" {
		return nil, ErrHostKeyNotPinned
	}
	var authMethods []ssh.AuthMethod
	if cfg.KeyPath != "" {
		if err := CheckLocalPath(cfg.KeyPath); err != nil {
			return nil, fmt.Errorf("SFTP 私钥文件: %w", err)
		}
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

	// The ssh transport must do its IO through activityConn, or an armed
	// deadline never slides and a long transfer is cut off after timeout.
	ac := &activityConn{Conn: conn, timeout: timeout}

	// The ssh handshake and the sftp INIT exchange block on server replies;
	// keep an absolute deadline armed until the client is fully up. Steady
	// state runs with no deadline — arming happens per operation instead.
	conn.SetDeadline(time.Now().Add(timeout))
	sshTransport, chans, reqs, err := ssh.NewClientConn(ac, addr, &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            authMethods,
		HostKeyCallback: pinnedHostKey(cfg.HostKey),
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
		conn:     ac,
		basePath: cfg.BasePath,
	}, nil
}

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
	s.conn.arm()
	defer s.conn.disarm()
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
	s.conn.arm()
	defer s.conn.disarm()
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
	s.conn.arm()
	f, err := s.client.Open(fullPath)
	s.conn.disarm()
	if err != nil {
		s.mu.Unlock()
		return nil, &OpError{Protocol: "sftp", Op: "open", Path: p, Err: err}
	}
	return &sftpLockedFile{mu: &s.mu, conn: s.conn, f: f, path: p}, nil
}

// sftpLockedFile holds the parent connection's mutex from Open until Close;
// each Read/Close brackets itself with arm/disarm on the shared transport,
// and IO progress inside the call keeps the deadline sliding.
type sftpLockedFile struct {
	mu   *sync.Mutex
	conn *activityConn
	f    *sftp.File
	path string
	once sync.Once
}

func (l *sftpLockedFile) Read(p []byte) (int, error) {
	l.conn.arm()
	n, err := l.f.Read(p)
	l.conn.disarm()
	if err != nil && err != io.EOF {
		err = &OpError{Protocol: "sftp", Op: "read", Path: l.path, Err: err}
	}
	return n, err
}

func (l *sftpLockedFile) Close() error {
	var err error
	l.once.Do(func() {
		l.conn.arm()
		err = l.f.Close()
		l.conn.disarm()
		l.mu.Unlock()
	})
	return err
}

func (s *SFTPFS) MoveFile(_ context.Context, src, dst string) error {
	srcFull, err := s.resolve(src)
	if err != nil {
		return err
	}
	dstFull, err := s.resolve(dst)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn.arm()
	defer s.conn.disarm()
	if _, err := s.client.Lstat(dstFull); err == nil {
		return existErr("sftp", "move", dst)
	}
	if err := s.client.Rename(srcFull, dstFull); err != nil {
		return &OpError{Protocol: "sftp", Op: "move", Path: src, Err: err}
	}
	return nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn.arm()
	defer s.conn.disarm()

	srcFile, err := s.client.Open(srcFull)
	if err != nil {
		return &OpError{Protocol: "sftp", Op: "open", Path: src, Err: err}
	}
	defer srcFile.Close()
	st, err := srcFile.Stat()
	if err != nil {
		return &OpError{Protocol: "sftp", Op: "stat", Path: src, Err: err}
	}
	if st.IsDir() {
		return &OpError{Protocol: "sftp", Op: "copy", Path: src, Err: ErrIsDir}
	}

	dstFile, err := s.client.OpenFile(dstFull, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		if _, serr := s.client.Lstat(dstFull); serr == nil {
			return existErr("sftp", "create", dst)
		}
		return &OpError{Protocol: "sftp", Op: "create", Path: dst, Err: err}
	}
	n, err := io.Copy(dstFile, srcFile)
	if cerr := dstFile.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != st.Size() {
		err = shortCopyErr("sftp", dst, n, st.Size())
	}
	if err != nil {
		s.client.Remove(dstFull)
		if _, ok := err.(*OpError); !ok {
			err = &OpError{Protocol: "sftp", Op: "copy", Path: dst, Err: err}
		}
		return err
	}
	_ = s.client.Chtimes(dstFull, st.ModTime(), st.ModTime())
	return nil
}

func (s *SFTPFS) Delete(_ context.Context, p string) error {
	fullPath, err := s.resolve(p)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn.arm()
	defer s.conn.disarm()
	if err := s.client.Remove(fullPath); err != nil {
		return &OpError{Protocol: "sftp", Op: "delete", Path: p, Err: err}
	}
	return nil
}

func (s *SFTPFS) MkdirAll(_ context.Context, p string) error {
	fullPath, err := s.resolve(p)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn.arm()
	defer s.conn.disarm()
	if err := s.client.MkdirAll(fullPath); err != nil {
		return &OpError{Protocol: "sftp", Op: "mkdir", Path: p, Err: err}
	}
	return nil
}

func (s *SFTPFS) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client.Close()
	return s.sshConn.Close()
}

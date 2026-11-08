package remotefs

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"FileEngine/internal/config"

	"golang.org/x/crypto/ssh"
)

// Every operation of every remote backend — reads, writes, both sides of a
// rename/copy, MkdirAll — must refuse a path escaping BasePath before any
// I/O. The backends are constructed bare (no client): reaching the client
// would panic, so a clean error proves the check runs first.
func TestBackendOpsRejectEscapesBeforeIO(t *testing.T) {
	ctx := context.Background()
	backends := map[string]RemoteFS{
		"sftp": &SFTPFS{basePath: "/vol1/data"},
		"ftp":  &FTPFS{basePath: "/srv/ftp"},
		"nfs":  &NFSFS{basePath: "exports/data"},
		"smb":  &SMBFS{basePath: "/share/sub"},
	}
	escapes := []string{"../etc/passwd", "/etc/passwd", "a/../../x"}
	for name, b := range backends {
		for _, bad := range escapes {
			ops := map[string]func() error{
				"List":     func() error { _, err := b.List(ctx, bad); return err },
				"Stat":     func() error { _, err := b.Stat(ctx, bad); return err },
				"ReadFile": func() error { _, err := b.ReadFile(ctx, bad); return err },
				"Move src": func() error { return b.MoveFile(ctx, bad, "ok/x") },
				"Move dst": func() error { return b.MoveFile(ctx, "ok/x", bad) },
				"Copy src": func() error { return b.CopyFile(ctx, bad, "ok/x") },
				"Copy dst": func() error { return b.CopyFile(ctx, "ok/x", bad) },
				"Delete":   func() error { return b.Delete(ctx, bad) },
				"MkdirAll": func() error { return b.MkdirAll(ctx, bad) },
			}
			for op, fn := range ops {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("%s %s(%q): reached I/O before confinement (panic %v)", name, op, bad, r)
						}
					}()
					if err := fn(); err == nil {
						t.Fatalf("%s %s(%q): escape accepted", name, op, bad)
					}
				}()
			}
		}
	}
}

func TestLocalPolicy(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "appdata")
	docs := filepath.Join(root, "docs")
	for _, d := range []string{data, docs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(data, "fileengine.db"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetLocalPolicy(LocalPolicy{}) })

	// Default (no roots): LOCAL disabled.
	_ = SetLocalPolicy(LocalPolicy{})
	if _, err := NewFromConfig(config.RemoteFSConfig{Protocol: "local", BasePath: docs}); !errors.Is(err, ErrLocalDisabled) {
		t.Fatalf("LOCAL with no roots: got %v, want ErrLocalDisabled", err)
	}

	if err := SetLocalPolicy(LocalPolicy{Roots: []string{docs, root}, Denied: []string{data}}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFromConfig(config.RemoteFSConfig{Protocol: "local", BasePath: "/etc"}); err == nil {
		t.Fatal("base path outside roots accepted")
	}
	if _, err := NewFromConfig(config.RemoteFSConfig{Protocol: "local", BasePath: data}); err == nil {
		t.Fatal("base path inside the data dir accepted")
	}
	// A base that contains the data dir is allowed, but the data dir is
	// hidden from listings and refused per operation.
	fs, err := NewFromConfig(config.RemoteFSConfig{Protocol: "local", BasePath: root})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.List(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == "appdata" {
			t.Fatal("data dir visible in listing")
		}
	}
	if _, err := fs.ReadFile(context.Background(), "appdata/fileengine.db"); err == nil {
		t.Fatal("DB file readable through LOCAL backend")
	}
	if _, err := fs.ReadFile(context.Background(), "docs/a.txt"); err != nil {
		t.Fatalf("legal read refused: %v", err)
	}

	// SFTP key files follow the same policy (checked before any dial).
	_, err = NewFromConfig(config.RemoteFSConfig{Protocol: "sftp", Host: "127.0.0.1", Port: 1, HostKey: "SHA256:x", KeyPath: filepath.Join(data, "fileengine.db")})
	if err == nil || !strings.Contains(err.Error(), "私钥") {
		t.Fatalf("key path in data dir: got %v", err)
	}
}

// startSSHServer runs a bare SSH server (no auth, no channels) presenting a
// fresh ed25519 host key; returns its address and host key fingerprint.
func startSSHServer(t *testing.T) (string, int, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					c.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					ch.Reject(ssh.Prohibited, "no channels")
				}
			}()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, ssh.FingerprintSHA256(signer.PublicKey())
}

func TestSFTPHostKeyTOFU(t *testing.T) {
	host, port, fp := startSSHServer(t)

	got, err := FetchSSHHostKey(host, port)
	if err != nil || got != fp {
		t.Fatalf("FetchSSHHostKey = %q, %v; want %q", got, err, fp)
	}

	base := config.RemoteFSConfig{Protocol: "sftp", Host: host, Port: port, Username: "u"}

	if _, err := NewSFTPFS(base); !errors.Is(err, ErrHostKeyNotPinned) {
		t.Fatalf("unpinned: got %v, want ErrHostKeyNotPinned", err)
	}

	wrong := base
	wrong.HostKey = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	_, err = NewSFTPFS(wrong)
	var mm *HostKeyMismatchError
	if !errors.As(err, &mm) || !strings.Contains(err.Error(), "服务器指纹已变化") || mm.Got != fp {
		t.Fatalf("mismatch: got %v", err)
	}

	// Matching pin passes the host-key check; the bare server then refuses
	// the sftp channel, so the failure must not be a host-key error.
	ok := base
	ok.HostKey = fp
	_, err = NewSFTPFS(ok)
	if err == nil || errors.As(err, &mm) || errors.Is(err, ErrHostKeyNotPinned) {
		t.Fatalf("matching pin: got %v, want a non-host-key failure", err)
	}
}

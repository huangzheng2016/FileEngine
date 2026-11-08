package remotefs

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"FileEngine/internal/config"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// throttledConn delays every server write while slow is set: the transfer
// keeps progressing, just slower than the client's per-op timeout.
type throttledConn struct {
	net.Conn
	slow *atomic.Bool
}

func (c *throttledConn) Write(p []byte) (int, error) {
	if c.slow.Load() {
		time.Sleep(40 * time.Millisecond)
	}
	return c.Conn.Write(p)
}

func startSFTPServer(t *testing.T, slow *atomic.Bool) config.RemoteFSConfig {
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
	var wg sync.WaitGroup
	t.Cleanup(func() { ln.Close(); wg.Wait() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				serveSFTPConn(&throttledConn{Conn: c, slow: slow}, cfg)
			}()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return config.RemoteFSConfig{Protocol: "sftp", Host: addr.IP.String(), Port: addr.Port,
		Username: "u", HostKey: ssh.FingerprintSHA256(signer.PublicKey()), BasePath: t.TempDir()}
}

func serveSFTPConn(c net.Conn, cfg *ssh.ServerConfig) {
	defer c.Close()
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, in, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			for req := range in {
				ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
				req.Reply(ok, nil)
				if ok {
					go func() {
						srv, err := sftp.NewServer(ch)
						if err == nil {
							srv.Serve()
							srv.Close()
						}
						ch.Close()
					}()
				}
			}
		}()
	}
}

func TestSFTPDeadlineSlidesDuringTransfers(t *testing.T) {
	t.Setenv("FE_REMOTEFS_TIMEOUT_SEC", "1")
	var slow atomic.Bool
	cfg := startSFTPServer(t, &slow)
	data := bytes.Repeat([]byte("0123456789abcdef"), 16<<10) // 256 KiB
	if err := os.WriteFile(filepath.Join(cfg.BasePath, "big.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := NewSFTPFS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	ctx := context.Background()

	slow.Store(true)
	start := time.Now()
	if err := fs.CopyFile(ctx, "big.bin", "copy.bin"); err != nil {
		t.Fatalf("slow but progressing copy failed after %v: %v", time.Since(start), err)
	}
	if el := time.Since(start); el < 1500*time.Millisecond {
		t.Fatalf("copy took %v; the throttle must exceed the 1s timeout for this test to mean anything", el)
	}
	slow.Store(false)
	got, err := os.ReadFile(filepath.Join(cfg.BasePath, "copy.bin"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("copy content mismatch: %v", err)
	}
}

func TestSFTPConcurrentWrites(t *testing.T) {
	var slow atomic.Bool
	cfg := startSFTPServer(t, &slow)
	fs, err := NewSFTPFS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if err := os.WriteFile(filepath.Join(cfg.BasePath, fmt.Sprintf("f%d.txt", i)), []byte(fmt.Sprint(i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dir := fmt.Sprintf("d%d/sub", i)
			src := fmt.Sprintf("f%d.txt", i)
			steps := []func() error{
				func() error { return fs.MkdirAll(ctx, dir) },
				func() error { return fs.CopyFile(ctx, src, dir+"/c.txt") },
				func() error { return fs.MoveFile(ctx, src, dir+"/m.txt") },
				func() error { _, err := fs.List(ctx, dir); return err },
				func() error { return fs.Delete(ctx, dir+"/c.txt") },
			}
			for _, step := range steps {
				if err := step(); err != nil {
					errs <- err
					return
				}
			}
			r, err := fs.ReadFile(ctx, dir+"/m.txt")
			if err != nil {
				errs <- err
				return
			}
			b, err := io.ReadAll(r)
			r.Close()
			if err != nil || string(b) != fmt.Sprint(i) {
				errs <- fmt.Errorf("worker %d read %q: %v", i, b, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if err := fs.CopyFile(ctx, "d0/sub/m.txt", "d1/sub/m.txt"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("copy onto an existing file must fail with ErrExist, got %v", err)
	}
}

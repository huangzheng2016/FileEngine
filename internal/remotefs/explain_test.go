package remotefs

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"FileEngine/internal/config"

	"golang.org/x/crypto/ssh"
)

func TestExplain(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&OpError{Protocol: "sftp", Op: "dial", Path: "h", Err: errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password]")}, "用户名或密码不对"},
		{&OpError{Protocol: "smb", Op: "dial", Err: errors.New("response error: The attempted logon is invalid.")}, "用户名或密码不对"},
		{&OpError{Protocol: "smb", Op: "mount", Err: errors.New("response error: {Network Name Not Found} The specified share name cannot be found")}, "找不到这个共享文件夹"},
		{errors.New("dial tcp 192.168.1.10:22: connect: connection refused"), "对方拒绝连接"},
		{errors.New("dial tcp: lookup nas.invalid: no such host"), "找不到这个地址"},
		{&OpError{Protocol: "nfs", Op: "mount", Err: errors.New("mount: MNT3ERR_ACCES")}, "NFS 权限"},
		{&OpError{Protocol: "ftp", Op: "dial", Err: errors.New("530 Login incorrect.")}, "用户名或密码不对"},
		{&OpError{Protocol: "sftp", Op: "dial", Err: os.ErrDeadlineExceeded}, "连接超时"},
		{ErrKeyPassphraseMissing, "私钥口令"},
		{errors.New("something odd"), "连接失败：something odd"},
	}
	for _, c := range cases {
		if got := Explain(c.err); !strings.Contains(got, c.want) {
			t.Errorf("Explain(%v) = %q, want it to contain %q", c.err, got, c.want)
		}
	}
}

func TestParsePrivateKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	locked, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	plainPEM := string(pem.EncodeToMemory(plain))
	lockedPEM := string(pem.EncodeToMemory(locked))

	// Pasted keys often lose the final newline or gain surrounding blanks.
	if _, err := ParsePrivateKey("\n  "+strings.TrimSpace(plainPEM)+"  \n", ""); err != nil {
		t.Fatalf("plain key: %v", err)
	}
	if _, err := ParsePrivateKey(plainPEM, "ignored"); err != nil {
		t.Fatalf("plain key with a passphrase: %v", err)
	}
	if _, err := ParsePrivateKey(lockedPEM, "s3cret"); err != nil {
		t.Fatalf("locked key: %v", err)
	}
	for _, c := range []struct {
		pem, pass string
		want      error
	}{
		{lockedPEM, "", ErrKeyPassphraseMissing},
		{lockedPEM, "wrong", ErrKeyPassphraseWrong},
		{"ssh-ed25519 AAAA… me@host", "", ErrKeyInvalid},
	} {
		if _, err := ParsePrivateKey(c.pem, c.pass); !errors.Is(err, c.want) {
			t.Errorf("ParsePrivateKey(pass=%q) = %v, want %v", c.pass, err, c.want)
		}
	}
	if s := fmt.Sprint(ErrKeyInvalid); !strings.Contains(s, "BEGIN") {
		t.Errorf("invalid-key message should tell what to paste: %q", s)
	}
}

func TestSFTPKeyAuth(t *testing.T) {
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostPriv)
	clientPub, clientPriv, _ := ed25519.GenerateKey(rand.Reader)
	allowed, _ := ssh.NewPublicKey(clientPub)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if string(k.Marshal()) == string(allowed.Marshal()) {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	cfg.AddHostKey(hostSigner)
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
	block, _ := ssh.MarshalPrivateKey(clientPriv, "")
	base := config.RemoteFSConfig{Protocol: "sftp", Host: "127.0.0.1", Port: addr.Port, Username: "u",
		HostKey: ssh.FingerprintSHA256(hostSigner.PublicKey())}

	good := base
	good.PrivateKey = string(pem.EncodeToMemory(block))
	// Authentication succeeds; the bare server then refuses the sftp channel.
	if _, err := NewSFTPFS(good); err == nil || strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("key auth: %v", err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	block, _ = ssh.MarshalPrivateKey(other, "")
	bad := base
	bad.PrivateKey = string(pem.EncodeToMemory(block))
	if _, err := NewSFTPFS(bad); err == nil || !strings.Contains(Explain(err), "用户名或密码不对") {
		t.Fatalf("wrong key: %v / %s", err, Explain(err))
	}
}

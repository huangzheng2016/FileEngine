package remotefs

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"FileEngine/internal/config"
)

// fakeFTPServer scripts just enough FTP for jlaffaye/ftp: banner, login,
// FEAT (no features advertised, so the client uses plain LIST), TYPE, OPTS,
// EPSV data connections and LIST transfers. Once a control connection has
// completed poisonAfter LISTs it goes silently dead — reads commands but
// never replies — simulating the hung-peer failure mode.
type fakeFTPServer struct {
	ln          net.Listener
	conns       atomic.Int32
	poisonAfter int32
}

func newFakeFTPServer(t *testing.T, poisonAfter int32) *fakeFTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeFTPServer{ln: ln, poisonAfter: poisonAfter}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.conns.Add(1)
			go s.handle(conn)
		}
	}()
	return s
}

func (s *fakeFTPServer) addr() (string, int) {
	a := s.ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

func (s *fakeFTPServer) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	fmt.Fprintf(conn, "220 FakeFTP ready\r\n")
	var dataLn net.Listener
	var lists int32
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if s.poisonAfter > 0 && lists >= s.poisonAfter {
			continue // silently dead peer
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "USER"):
			fmt.Fprintf(conn, "331 Password required\r\n")
		case strings.HasPrefix(cmd, "PASS"):
			fmt.Fprintf(conn, "230 Logged in\r\n")
		case strings.HasPrefix(cmd, "FEAT"):
			fmt.Fprintf(conn, "211-Features:\r\n211 End\r\n")
		case strings.HasPrefix(cmd, "TYPE"):
			fmt.Fprintf(conn, "200 Type set\r\n")
		case strings.HasPrefix(cmd, "OPTS"):
			fmt.Fprintf(conn, "200 OK\r\n")
		case strings.HasPrefix(cmd, "EPSV"):
			var err error
			dataLn, err = net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				fmt.Fprintf(conn, "425 Cannot open data conn\r\n")
				continue
			}
			fmt.Fprintf(conn, "229 Entering Extended Passive Mode (|||%d|)\r\n",
				dataLn.Addr().(*net.TCPAddr).Port)
		case strings.HasPrefix(cmd, "LIST"):
			if dataLn == nil {
				fmt.Fprintf(conn, "425 No data conn\r\n")
				continue
			}
			fmt.Fprintf(conn, "150 Here comes the listing\r\n")
			if dc, err := dataLn.Accept(); err == nil {
				fmt.Fprintf(dc, "-rw-r--r-- 1 owner group 213 Jan 29 21:24 hello.txt\r\n")
				dc.Close()
			}
			dataLn.Close()
			dataLn = nil
			fmt.Fprintf(conn, "226 Transfer complete\r\n")
			lists++
		case strings.HasPrefix(cmd, "QUIT"):
			fmt.Fprintf(conn, "221 Bye\r\n")
			return
		default:
			fmt.Fprintf(conn, "502 Not implemented\r\n")
		}
	}
}

func TestFTPRedialsAfterTimeout(t *testing.T) {
	t.Setenv("FE_REMOTEFS_TIMEOUT_SEC", "1")
	srv := newFakeFTPServer(t, 1) // go silent after the first successful LIST
	host, port := srv.addr()
	fs, err := NewFTPFS(config.RemoteFSConfig{
		Host: host, Port: port, Username: "u", Password: "p", BasePath: "/data",
	})
	if err != nil {
		t.Fatalf("NewFTPFS: %v", err)
	}
	defer fs.Close()
	ctx := context.Background()

	entries, err := fs.List(ctx, ".")
	if err != nil {
		t.Fatalf("List #1: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "hello.txt" {
		t.Fatalf("List #1: got %v, want [hello.txt]", entries)
	}

	if _, err := fs.List(ctx, "."); !IsTimeout(err) {
		t.Fatalf("List #2: got %v, want a timeout", err)
	}

	entries, err = fs.List(ctx, ".")
	if err != nil {
		t.Fatalf("List #3 (after re-dial): %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "hello.txt" {
		t.Fatalf("List #3: got %v, want [hello.txt]", entries)
	}
	if got := srv.conns.Load(); got != 2 {
		t.Fatalf("server saw %d control connections, want 2 (initial + re-dial)", got)
	}
}

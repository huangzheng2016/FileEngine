package remotefs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"FileEngine/internal/config"
)

// ftpFake is an in-memory FTP server with just enough commands for the
// executor's operations. Features are advertised verbatim in FEAT.
type ftpFake struct {
	ln       net.Listener
	conns    atomic.Int32
	features []string
	hangStor bool // STOR stores the data, then never answers

	mu    sync.Mutex
	files map[string][]byte
	cmds  []string
}

func newFTPFake(t *testing.T, features ...string) *ftpFake {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &ftpFake{ln: ln, features: features, files: map[string][]byte{}}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.conns.Add(1)
			go s.handle(c)
		}
	}()
	return s
}

func (s *ftpFake) put(p, data string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[p] = []byte(data)
}

func (s *ftpFake) get(p string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.files[p]
	return string(b), ok
}

func (s *ftpFake) count(verb string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.cmds {
		if strings.HasPrefix(c, verb) {
			n++
		}
	}
	return n
}

func (s *ftpFake) config(t *testing.T, user string) config.RemoteFSConfig {
	a := s.ln.Addr().(*net.TCPAddr)
	return config.RemoteFSConfig{Host: a.IP.String(), Port: a.Port, Username: user, Password: "p", BasePath: "/data"}
}

func (s *ftpFake) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(f string, a ...any) { fmt.Fprintf(conn, f+"\r\n", a...) }
	reply("220 fake")
	var dataLn net.Listener
	dataConn := func() net.Conn {
		if dataLn == nil {
			return nil
		}
		dc, err := dataLn.Accept()
		dataLn.Close()
		dataLn = nil
		if err != nil {
			return nil
		}
		return dc
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb, arg, _ := strings.Cut(line, " ")
		verb = strings.ToUpper(verb)
		s.mu.Lock()
		s.cmds = append(s.cmds, strings.TrimSpace(verb+" "+arg))
		s.mu.Unlock()
		switch verb {
		case "USER":
			reply("331 need password")
		case "PASS":
			reply("230 ok")
		case "FEAT":
			if len(s.features) == 0 {
				reply("211 no features")
				break
			}
			fmt.Fprintf(conn, "211-Features:\r\n")
			for _, f := range s.features {
				fmt.Fprintf(conn, " %s\r\n", f)
			}
			reply("211 End")
		case "TYPE", "OPTS":
			reply("200 ok")
		case "EPSV":
			dataLn, _ = net.Listen("tcp", "127.0.0.1:0")
			reply("229 Entering Extended Passive Mode (|||%d|)", dataLn.Addr().(*net.TCPAddr).Port)
		case "LIST":
			reply("150 listing")
			if dc := dataConn(); dc != nil {
				s.mu.Lock()
				var names []string
				for p, b := range s.files {
					if path.Dir(p) == strings.TrimSuffix(arg, "/") {
						names = append(names, fmt.Sprintf("-rw-r--r-- 1 o g %d Jan 29 21:24 %s", len(b), path.Base(p)))
					}
				}
				s.mu.Unlock()
				sort.Strings(names)
				for _, n := range names {
					fmt.Fprintf(dc, "%s\r\n", n)
				}
				dc.Close()
			}
			reply("226 done")
		case "MLST":
			if b, ok := s.get(arg); ok {
				fmt.Fprintf(conn, "250-details\r\n Type=file;Size=%d;Modify=20240101000000; %s\r\n250 End\r\n", len(b), arg)
			} else {
				reply("550 no such file")
			}
		case "SIZE":
			if b, ok := s.get(arg); ok {
				reply("213 %d", len(b))
			} else {
				reply("550 no such file")
			}
		case "MDTM":
			reply("213 20240101000000")
		case "MFMT":
			reply("213 ok")
		case "RETR":
			b, ok := s.get(arg)
			if !ok {
				reply("550 no such file")
				break
			}
			reply("150 sending")
			if dc := dataConn(); dc != nil {
				io.WriteString(dc, b)
				dc.Close()
			}
			reply("226 done")
		case "STOR":
			reply("150 receiving")
			if dc := dataConn(); dc != nil {
				var buf bytes.Buffer
				io.Copy(&buf, dc)
				dc.Close()
				s.mu.Lock()
				s.files[arg] = buf.Bytes()
				s.mu.Unlock()
			}
			if s.hangStor {
				time.Sleep(10 * time.Second)
				return
			}
			reply("226 done")
		case "DELE":
			s.mu.Lock()
			_, ok := s.files[arg]
			delete(s.files, arg)
			s.mu.Unlock()
			if ok {
				reply("250 deleted")
			} else {
				reply("550 no such file")
			}
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 not implemented")
		}
	}
}

func TestFTPStatFallsBackToListWithoutMLST(t *testing.T) {
	srv := newFTPFake(t)
	srv.put("/data/a.txt", "hello")
	fs, err := NewFTPFS(srv.config(t, "u"))
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	ctx := context.Background()
	st, err := fs.Stat(ctx, "a.txt")
	if err != nil || st.Size != 5 || st.IsDir {
		t.Fatalf("Stat existing: %+v %v", st, err)
	}
	if _, err := fs.Stat(ctx, "missing.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat missing: %v, want ErrNotExist", err)
	}
}

func TestFTPEmptyUsernameLogsInAnonymouslyAndUsesBinary(t *testing.T) {
	srv := newFTPFake(t)
	fs, err := NewFTPFS(srv.config(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	if srv.count("USER anonymous") != 1 || srv.count("TYPE I") != 1 {
		t.Fatalf("commands: %v", srv.cmds)
	}
}

func TestFTPCopyReusesSourceConnectionAndSkipsListWithMLST(t *testing.T) {
	srv := newFTPFake(t, "MLST type*;size*;modify*;")
	for i := 0; i < 3; i++ {
		srv.put(fmt.Sprintf("/data/in%d.txt", i), strconv.Itoa(i))
	}
	fs, err := NewFTPFS(srv.config(t, "u"))
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := fs.CopyFile(ctx, fmt.Sprintf("in%d.txt", i), fmt.Sprintf("out%d.txt", i)); err != nil {
			t.Fatalf("copy %d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		if got, ok := srv.get(fmt.Sprintf("/data/out%d.txt", i)); !ok || got != strconv.Itoa(i) {
			t.Fatalf("out%d = %q %v", i, got, ok)
		}
	}
	if got := srv.conns.Load(); got != 2 {
		t.Fatalf("control connections = %d, want 2 (primary + one reused source)", got)
	}
	if n := srv.count("LIST"); n != 0 {
		t.Fatalf("%d LISTs issued; MLST should answer destination checks", n)
	}
	if err := fs.CopyFile(ctx, "in0.txt", "out0.txt"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("copy over existing: %v, want ErrExist", err)
	}
}

func TestFTPCopyKeepsSourceModTimeWhenSupported(t *testing.T) {
	srv := newFTPFake(t, "MLST type*;size*;modify*;", "MFMT", "MDTM")
	srv.put("/data/a.txt", "x")
	fs, err := NewFTPFS(srv.config(t, "u"))
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	if err := fs.CopyFile(context.Background(), "a.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	if srv.count("MFMT") != 1 {
		t.Fatalf("commands: %v", srv.cmds)
	}
}

func TestFTPCopyTimeoutRemovesPartialDestination(t *testing.T) {
	t.Setenv("FE_REMOTEFS_TIMEOUT_SEC", "1")
	srv := newFTPFake(t, "MLST type*;size*;modify*;")
	srv.hangStor = true
	srv.put("/data/a.txt", "payload")
	fs, err := NewFTPFS(srv.config(t, "u"))
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	err = fs.CopyFile(context.Background(), "a.txt", "b.txt")
	if !IsTimeout(err) {
		t.Fatalf("copy: %v, want a timeout", err)
	}
	if _, ok := srv.get("/data/b.txt"); ok {
		t.Fatal("partial destination left on the server")
	}
}

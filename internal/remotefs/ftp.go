package remotefs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"sync"
	"time"

	"FileEngine/internal/config"

	"github.com/jlaffaye/ftp"
)

// FTPFS serializes control-connection commands with mu: a *ftp.ServerConn is
// not safe for concurrent use, and eino executes parallel tool calls
// concurrently. Data transfers (the Retr response stream) run on their own
// TCP connection and stay parallel; only command/response framing is locked.
type FTPFS struct {
	mu       sync.Mutex
	conn     *ftp.ServerConn
	basePath string
}

func NewFTPFS(cfg config.RemoteFSConfig) (*FTPFS, error) {
	port := cfg.Port
	if port == 0 {
		port = 21
	}

	conn, err := ftp.Dial(fmt.Sprintf("%s:%d", cfg.Host, port), ftp.DialWithTimeout(10*time.Second))
	if err != nil {
		return nil, fmt.Errorf("ftp dial: %w", err)
	}

	if cfg.Username != "" {
		if err := conn.Login(cfg.Username, cfg.Password); err != nil {
			conn.Quit()
			return nil, fmt.Errorf("ftp login: %w", err)
		}
	}

	return &FTPFS{conn: conn, basePath: cfg.BasePath}, nil
}

// resolve maps a caller path to a host slash path confined to basePath
// (absolute paths must stay inside it; ".." escapes are rejected).
func (f *FTPFS) resolve(p string) (string, error) {
	var full string
	if path.IsAbs(p) {
		full = path.Clean(p)
	} else {
		full = path.Join(f.basePath, p)
	}
	if err := confineSlashPath(full, f.basePath); err != nil {
		return "", err
	}
	return full, nil
}

func (f *FTPFS) List(_ context.Context, p string) ([]FileInfo, error) {
	fullPath, err := f.resolve(p)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	entries, err := f.conn.List(fullPath)
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var result []FileInfo
	for _, entry := range entries {
		if entry.Name == "." || entry.Name == ".." {
			continue
		}
		ft := FileTypeFile
		if entry.Type == ftp.EntryTypeFolder {
			ft = FileTypeDirectory
		} else if entry.Type == ftp.EntryTypeLink {
			ft = FileTypeSymlink
		}
		result = append(result, FileInfo{
			Name:     entry.Name,
			Path:     path.Join(p, entry.Name),
			Size:     int64(entry.Size),
			ModTime:  entry.Time,
			FileType: ft,
			IsDir:    entry.Type == ftp.EntryTypeFolder,
		})
	}
	return result, nil
}

func (f *FTPFS) Stat(_ context.Context, p string) (*FileInfo, error) {
	fullPath, err := f.resolve(p)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	entry, err := f.conn.GetEntry(fullPath)
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	ft := FileTypeFile
	if entry.Type == ftp.EntryTypeFolder {
		ft = FileTypeDirectory
	}
	return &FileInfo{
		Name:     entry.Name,
		Path:     p,
		Size:     int64(entry.Size),
		ModTime:  entry.Time,
		FileType: ft,
		IsDir:    entry.Type == ftp.EntryTypeFolder,
	}, nil
}

func (f *FTPFS) ReadFile(_ context.Context, p string) (io.ReadCloser, error) {
	fullPath, err := f.resolve(p)
	if err != nil {
		return nil, err
	}
	// Hold the control-connection lock for the whole transfer: jlaffaye/ftp
	// interleaves the 226 completion reply on the control connection, so any
	// other command issued between Retr and Close would read a stale reply
	// and desync the stream. Transfers serialize — same posture as NFS.
	f.mu.Lock()
	resp, err := f.conn.Retr(fullPath)
	if err != nil {
		f.mu.Unlock()
		return nil, err
	}
	return &ftpLockedResponse{mu: &f.mu, r: resp}, nil
}

// ftpLockedResponse holds the control-connection mutex from Retr until
// Close (once). Read itself needs no per-call locking — the lock is already
// held for the transfer's lifetime.
type ftpLockedResponse struct {
	mu   *sync.Mutex
	r    *ftp.Response
	once sync.Once
}

func (l *ftpLockedResponse) Read(p []byte) (int, error) { return l.r.Read(p) }

func (l *ftpLockedResponse) Close() error {
	var err error
	l.once.Do(func() {
		err = l.r.Close()
		l.mu.Unlock()
	})
	return err
}

func (f *FTPFS) MoveFile(_ context.Context, src, dst string) error {
	srcFull, err := f.resolve(src)
	if err != nil {
		return err
	}
	dstFull, err := f.resolve(dst)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn.Rename(srcFull, dstFull)
}

func (f *FTPFS) CopyFile(ctx context.Context, src, dst string) error {
	reader, err := f.ReadFile(ctx, src)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(reader)
	// Close BEFORE Stor: ReadFile holds the control-connection lock for the
	// transfer's lifetime; Stor needs it too (Close is idempotent).
	_ = reader.Close()
	if err != nil {
		return err
	}

	dstFull, err := f.resolve(dst)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn.Stor(dstFull, bytes.NewReader(data))
}

func (f *FTPFS) Delete(_ context.Context, p string) error {
	fullPath, err := f.resolve(p)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn.Delete(fullPath)
}

func (f *FTPFS) MkdirAll(_ context.Context, p string) error {
	fullPath, err := f.resolve(p)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn.MakeDir(fullPath)
}

func (f *FTPFS) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn.Quit()
}

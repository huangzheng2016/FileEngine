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
	f.mu.Lock()
	resp, err := f.conn.Retr(fullPath)
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &ftpLockedResponse{mu: &f.mu, r: resp}, nil
}

// ftpLockedResponse locks only Close: the transfer itself runs on its own
// data connection (parallel-safe), but closing it reads the completion reply
// on the shared control connection.
type ftpLockedResponse struct {
	mu *sync.Mutex
	r  *ftp.Response
}

func (l *ftpLockedResponse) Read(p []byte) (int, error) { return l.r.Read(p) }

func (l *ftpLockedResponse) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.Close()
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
	defer reader.Close()

	data, err := io.ReadAll(reader)
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

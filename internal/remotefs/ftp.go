package remotefs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"time"

	"FileEngine/internal/config"

	"github.com/jlaffaye/ftp"
)

type FTPFS struct {
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

func (f *FTPFS) resolve(p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(f.basePath, p)
}

func (f *FTPFS) List(_ context.Context, p string) ([]FileInfo, error) {
	entries, err := f.conn.List(f.resolve(p))
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
	entry, err := f.conn.GetEntry(f.resolve(p))
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
	resp, err := f.conn.Retr(f.resolve(p))
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (f *FTPFS) MoveFile(_ context.Context, src, dst string) error {
	return f.conn.Rename(f.resolve(src), f.resolve(dst))
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

	return f.conn.Stor(f.resolve(dst), bytes.NewReader(data))
}

func (f *FTPFS) MkdirAll(_ context.Context, p string) error {
	return f.conn.MakeDir(f.resolve(p))
}

func (f *FTPFS) Close() error {
	return f.conn.Quit()
}

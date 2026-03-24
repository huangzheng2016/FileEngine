package remotefs

import (
	"context"
	"io"
)

type RemoteFS interface {
	List(ctx context.Context, path string) ([]FileInfo, error)
	Stat(ctx context.Context, path string) (*FileInfo, error)
	ReadFile(ctx context.Context, path string) (io.ReadCloser, error)
	MoveFile(ctx context.Context, src, dst string) error
	CopyFile(ctx context.Context, src, dst string) error
	MkdirAll(ctx context.Context, path string) error
	Close() error
}

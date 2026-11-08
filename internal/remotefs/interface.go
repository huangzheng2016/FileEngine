package remotefs

import (
	"context"
	"io"
)

// The write operations follow the no-overwrite, no-tree-delete contract in
// write.go.
type RemoteFS interface {
	List(ctx context.Context, path string) ([]FileInfo, error)
	Stat(ctx context.Context, path string) (*FileInfo, error)
	ReadFile(ctx context.Context, path string) (io.ReadCloser, error)
	MoveFile(ctx context.Context, src, dst string) error
	CopyFile(ctx context.Context, src, dst string) error
	Delete(ctx context.Context, path string) error
	MkdirAll(ctx context.Context, path string) error
	Close() error
}

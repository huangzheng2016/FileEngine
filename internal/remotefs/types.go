package remotefs

import "time"

type FileType string

const (
	FileTypeFile      FileType = "file"
	FileTypeDirectory FileType = "directory"
	FileTypeSymlink   FileType = "symlink"
)

type FileInfo struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	Permissions string    `json:"permissions"`
	FileType    FileType  `json:"file_type"`
	IsDir       bool      `json:"is_dir"`
	// Handle is an opaque backend-specific locator for this entry (NFS: the
	// file handle from READDIRPLUS). It lets a follow-up listing skip the
	// per-component path resolution. Nil for backends without such a notion;
	// consumers must treat it as an optimization hint only.
	Handle []byte `json:"-"`
}

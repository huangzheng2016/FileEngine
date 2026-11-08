package remotefs

import (
	"fmt"

	"FileEngine/internal/config"
)

// The LocalPolicy and the SFTP host-key pin are enforced by the constructors
// it calls.
func NewFromConfig(cfg config.RemoteFSConfig) (RemoteFS, error) {
	switch cfg.Protocol {
	case "local":
		return newPolicedLocalFS(cfg.BasePath)
	case "sftp":
		return NewSFTPFS(cfg)
	case "ftp":
		return NewFTPFS(cfg)
	case "smb":
		return NewSMBFS(cfg)
	case "nfs":
		return NewNFSFS(cfg)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", cfg.Protocol)
	}
}

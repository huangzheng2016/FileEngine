package remotefs

import (
	"fmt"
	"path"
	"strings"
)

// Both sides are compared segment-wise from the root, so sibling prefixes
// ("/photo" vs "/photos2") do not bleed; an empty base confines nothing.
// Unlike LocalFS this cannot chase symlinks (FTP/NFS/SMB expose no portable
// readlink), so a symlink inside the base may still point outside.
func confineSlashPath(resolved, base string) error {
	b := strings.Trim(path.Clean("/"+base), "/")
	if b == "" {
		return nil
	}
	f := strings.Trim(path.Clean("/"+resolved), "/")
	if f != b && !strings.HasPrefix(f, b+"/") {
		return fmt.Errorf("path %q escapes base path %q", resolved, base)
	}
	return nil
}

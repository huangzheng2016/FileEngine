package remotefs

import (
	"fmt"
	"path"
	"strings"
)

// confineSlashPath checks that resolved — a slash-separated path a backend
// produced by joining a caller path onto its basePath — actually stays inside
// basePath. Both sides are normalized to root-anchored form and compared
// segment-wise, so sibling prefixes ("/photo" vs "/photos2") do not bleed.
// An empty base confines nothing: the whole reachable filesystem is the base.
//
// This is the remote counterpart of LocalFS.resolve's lexical check. Unlike
// LocalFS it cannot chase symlinks (FTP/NFS/SMB expose no portable readlink),
// so a symlink inside the base may still point outside — that residual risk
// is documented on the RemoteFS interface; the confinement here closes the
// much larger hole of absolute paths and ".." traversal escaping outright.
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

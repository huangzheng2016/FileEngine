// Package pathutil holds the path rules shared by every layer that writes or
// executes filesystem paths (agent tools, REST handlers, executor). It is a
// leaf package so none of those layers has to import another to validate.
package pathutil

import (
	"fmt"
	"path"
	"strings"
)

// ValidateFSPath checks that a target/category path is a legal, root-confined
// slash path. The executor resolves stored paths against the filesystem root
// (RemoteFS BasePath), so anything that could escape that root is rejected
// before it is written to the DB. Rules:
//   - "" is allowed (set_target / PATCH clear the target); callers that need a
//     non-empty path reject it via their own required-field handling
//   - no NUL bytes
//   - no ".." segments (absolute-form paths like "/../etc" would escape the
//     root on SFTP-style backends that treat leading "/" as host-absolute)
//   - must name an entry — the bare root ("/", ".") is not a valid target
func ValidateFSPath(p string) error {
	if p == "" {
		return nil
	}
	if strings.ContainsRune(p, 0) {
		return fmt.Errorf("path %q contains a NUL byte", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("path %q contains '..' and could escape the filesystem root", p)
		}
	}
	if cleaned := path.Clean(p); cleaned == "/" || cleaned == "." {
		return fmt.Errorf("path %q does not name a file or directory", p)
	}
	return nil
}

// RootRelative validates p (which must be non-empty) and returns it in the
// root-relative form the RemoteFS backends resolve against their BasePath:
// cleaned, without a leading "/". Stored paths are root-relative by contract
// ("/photos/a.jpg" means <BasePath>/photos/a.jpg), so a leading slash must
// never reach a backend that would read it as host-absolute.
func RootRelative(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if err := ValidateFSPath(p); err != nil {
		return "", err
	}
	return strings.TrimPrefix(path.Clean("/"+p), "/"), nil
}

// Package pathutil holds the path rules shared by every layer that writes or
// executes filesystem paths (agent tools, REST handlers, executor). It is a
// leaf package so none of those layers has to import another to validate.
package pathutil

import (
	"errors"
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
		return errors.New("路径不能包含 NUL 字符")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("路径「%s」不能包含 ..（会越出文件系统根目录）", p)
		}
	}
	if cleaned := path.Clean(p); cleaned == "/" || cleaned == "." {
		return fmt.Errorf("路径「%s」没有指向具体的文件或目录", p)
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
		return "", errors.New("路径为空")
	}
	if err := ValidateFSPath(p); err != nil {
		return "", err
	}
	return strings.TrimPrefix(path.Clean("/"+p), "/"), nil
}

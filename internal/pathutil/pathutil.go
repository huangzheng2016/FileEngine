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

// ValidateFSPath rejects anything that could escape the filesystem root
// before it is written to the DB. "" is allowed (it clears a target). ".."
// segments are refused because absolute forms like "/../etc" escape the root
// on SFTP-style backends that read a leading "/" as host-absolute.
func ValidateFSPath(p string) error {
	if p == "" {
		return nil
	}
	if strings.ContainsRune(p, 0) {
		return errors.New("路径不能包含 NUL 字符")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("路径「%s」不能包含 ..（会越出存储根目录）", p)
		}
	}
	if cleaned := path.Clean(p); cleaned == "/" || cleaned == "." {
		return fmt.Errorf("路径「%s」没有指向具体的文件或目录", p)
	}
	return nil
}

// Stored paths are root-relative ("/photos/a.jpg" means
// <BasePath>/photos/a.jpg), so a leading slash must never reach a backend
// that would read it as host-absolute.
func RootRelative(p string) (string, error) {
	if p == "" {
		return "", errors.New("路径为空")
	}
	if err := ValidateFSPath(p); err != nil {
		return "", err
	}
	return strings.TrimPrefix(path.Clean("/"+p), "/"), nil
}

package remotefs

import (
	"errors"
	"io/fs"
	"strings"
	"syscall"
)

// IsNotExist is the one definition of "absent" for callers that must tell it
// apart from "could not tell". A file where a directory should be counts as
// absent too (ENOTDIR).
func IsNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// IsCrossDevice reports whether a rename failed because source and target
// live on different volumes (or the backend cannot rename at all), as opposed
// to a transient failure that says nothing about later renames.
func IsCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EXDEV) || errors.Is(err, errors.ErrUnsupported) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"cross-device", "different device", "different disk drive", "nfs3err_xdev", "not same device"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

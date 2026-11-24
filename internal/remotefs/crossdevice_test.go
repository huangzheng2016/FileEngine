package remotefs

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestIsCrossDevice(t *testing.T) {
	yes := []error{
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EXDEV},
		fmt.Errorf("wrapped: %w", errors.ErrUnsupported),
		errors.New("rename: cross-device link"),
		errors.New("NFS3ERR_XDEV"),
		errors.New("The destination file of a rename request is located on a different device"),
	}
	for _, err := range yes {
		if !IsCrossDevice(err) {
			t.Errorf("%v: want cross-device", err)
		}
	}
	no := []error{nil, errors.New("i/o timeout"), os.ErrPermission, os.ErrExist}
	for _, err := range no {
		if IsCrossDevice(err) {
			t.Errorf("%v: not cross-device", err)
		}
	}
}

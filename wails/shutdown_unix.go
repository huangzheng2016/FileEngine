//go:build !windows

package main

import (
	"os"
	"syscall"
)

// terminateProcess asks the backend to exit gracefully; the caller falls
// back to Kill after a grace period. On Windows there is no SIGTERM
// equivalent, so terminateProcess kills directly (see shutdown_windows.go).
func terminateProcess(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}

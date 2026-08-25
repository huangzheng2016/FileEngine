package main

import "os"

// terminateProcess on Windows: there is no graceful per-process signal, so
// the only option is to kill. Kept behind the same helper so stopBackend is
// platform-neutral.
func terminateProcess(p *os.Process) error {
	return p.Kill()
}

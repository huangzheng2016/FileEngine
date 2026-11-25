package scanner

import (
	"context"
	"sync/atomic"

	"FileEngine/internal/remotefs"
)

// After this many transport timeouts in a row a post pass gives up: the
// storage is unreachable, and every remaining file would wait out its own
// timeout. A var so tests can shrink it.
var maxConsecutiveTimeouts int32 = 32

// timeoutGuard stops one post pass (not the scan) once the storage keeps
// timing out; any other outcome proves the connection alive and resets it.
type timeoutGuard struct {
	run     atomic.Int32
	tripped atomic.Bool
	abort   context.CancelFunc
}

func (g *timeoutGuard) observe(err error) {
	if err == nil || !remotefs.IsTimeout(err) {
		g.run.Store(0)
		return
	}
	if g.run.Add(1) >= maxConsecutiveTimeouts && g.tripped.CompareAndSwap(false, true) {
		g.abort()
	}
}

func (s *Scanner) addNote(note string) {
	s.skippedMu.Lock()
	defer s.skippedMu.Unlock()
	s.notes = append(s.notes, note)
}

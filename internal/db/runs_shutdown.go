package db

// CancelAllRuns cancels every registered run (graceful shutdown) and returns
// how many were live.
func CancelAllRuns() int {
	runCancels.mu.Lock()
	cancels := make([]func(), 0, len(runCancels.m))
	for _, c := range runCancels.m {
		cancels = append(cancels, c)
	}
	runCancels.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	return len(cancels)
}

// LiveRunCount is the number of runs still registered (not yet finished).
func LiveRunCount() int {
	runCancels.mu.Lock()
	defer runCancels.mu.Unlock()
	return len(runCancels.m)
}

package db

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

func LiveRunCount() int {
	runCancels.mu.Lock()
	defer runCancels.mu.Unlock()
	return len(runCancels.m)
}

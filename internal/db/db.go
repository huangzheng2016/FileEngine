package db

import (
	"fmt"
	"log"

	dal "FileEngine/biz/dal/db"
	"FileEngine/internal/config"
)

// Open initializes the database and sweeps what a previous process lifetime
// left live: scans and runs still marked running.
func Open(cfg config.DatabaseConfig) error {
	// Scheduled counter refreshes emit through the package handle Init
	// replaces; they finish against the handle they were scheduled for.
	FlushCounts()
	if err := dal.Init(cfg); err != nil {
		return err
	}
	repo := NewRepository(dal.DB())
	if n, err := repo.MarkInterruptedScans(); err != nil {
		return fmt.Errorf("failed to mark interrupted scans: %w", err)
	} else if n > 0 {
		log.Printf("db init: marked %d zombie scanning session(s) as interrupted", n)
	}
	if n, err := repo.SweepStaleRuns(); err != nil {
		return fmt.Errorf("failed to sweep stale runs: %w", err)
	} else if n > 0 {
		log.Printf("db init: marked %d stale running run(s) as stopped", n)
	}
	return nil
}

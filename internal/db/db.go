package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"FileEngine/internal/config"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Init(cfg config.DatabaseConfig) error {
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "sqlite":
		// Enable WAL mode and shared cache via DSN params
		dsn := cfg.DSN + "?_journal_mode=WAL&_busy_timeout=30000&_synchronous=NORMAL&_cache_size=-20000&_foreign_keys=ON&_txlock=immediate"
		dialector = sqlite.Open(dsn)
	case "mysql":
		dialector = mysql.Open(cfg.DSN)
	case "postgres":
		dialector = postgres.Open(cfg.DSN)
	default:
		return fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	var err error
	DB, err = gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("failed to connect database: %w", err)
	}

	if cfg.Driver == "sqlite" {
		sqlDB, err := DB.DB()
		if err != nil {
			return fmt.Errorf("failed to get underlying sql.DB: %w", err)
		}
		// Single writer connection to avoid lock contention
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(0)

		// Additional pragmas
		pragmas := []string{
			"PRAGMA temp_store=MEMORY",
			"PRAGMA mmap_size=268435456", // 256MB mmap
		}
		for _, p := range pragmas {
			if _, err := sqlDB.Exec(p); err != nil {
				return fmt.Errorf("failed to set %s: %w", p, err)
			}
		}
	}

	if err := DB.AutoMigrate(&FileEntry{}, &Category{}, &Filesystem{}, &ScanSession{}, &AgentLog{}, &ModelProvider{}, &ExecOp{}); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	// Backfill context_window for providers created before the field existed
	// (max_tokens was previously misused as the window; it now only caps output).
	if err := backfillContextWindow(DB); err != nil {
		return fmt.Errorf("failed to backfill context_window: %w", err)
	}

	// Zombie sweep: sessions still "scanning" belong to a previous process
	// lifetime — mark them interrupted so they cannot masquerade as live.
	if n, err := NewRepository(DB).MarkInterruptedScans(); err != nil {
		return fmt.Errorf("failed to mark interrupted scans: %w", err)
	} else if n > 0 {
		log.Printf("db init: marked %d zombie scanning session(s) as interrupted", n)
	}

	return nil
}

// backfillContextWindow sets the default context window on provider rows that
// predate the context_window column (NULL / non-positive values).
func backfillContextWindow(db *gorm.DB) error {
	return db.Model(&ModelProvider{}).
		Where("context_window IS NULL OR context_window <= 0").
		Update("context_window", DefaultContextWindow).Error
}

func GetDB() *gorm.DB {
	return DB
}

// WithRetry wraps a DB operation with retry logic for SQLite lock errors.
func WithRetry(fn func() error) error {
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		if isSQLiteLocked(err) && i < maxRetries-1 {
			wait := time.Duration(100*(i+1)) * time.Millisecond
			log.Printf("sqlite locked, retrying in %v (attempt %d/%d)", wait, i+1, maxRetries)
			time.Sleep(wait)
			continue
		}
		return err
	}
	return nil
}

func isSQLiteLocked(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return msg == "database is locked" ||
		msg == string(sql.ErrConnDone.Error()) ||
		contains(msg, "database is locked") ||
		contains(msg, "SQLITE_BUSY")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

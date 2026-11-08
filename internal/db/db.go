package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"FileEngine/internal/config"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

// readDB is the SQLite read-only connection pool (nil on MySQL/PostgreSQL,
// whose single pool already serves concurrent readers). See ReadDB.
var readDB *gorm.DB

// readPoolSize bounds the SQLite reader pool. WAL lets readers run alongside
// the single writer connection without blocking on it.
const readPoolSize = 4

// ReadDB returns the pool for read-only queries: on SQLite a separate
// query_only pool so long reads never queue behind the single writer
// connection (readers see the last committed state); elsewhere the main DB.
func ReadDB() *gorm.DB {
	if readDB != nil {
		return readDB
	}
	return DB
}

func Init(cfg config.DatabaseConfig) error {
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "sqlite":
		dialector = sqlite.Open(sqliteDSN(cfg.DSN))
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

	if err := DB.AutoMigrate(&FileEntry{}, &Category{}, &Filesystem{}, &ScanSession{}, &AgentLog{}, &ModelProvider{}); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}
	if err := DB.AutoMigrate(&SessionPlan{}, &MemoryItem{}); err != nil {
		return fmt.Errorf("failed to migrate plan/memory tables: %w", err)
	}

	readDB = nil
	if cfg.Driver == "sqlite" {
		// Opened after AutoMigrate: the file exists in WAL mode by now.
		if readDB, err = openSQLiteReadPool(cfg.DSN); err != nil {
			return err
		}
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

// sqliteDSN appends the per-connection pragmas for the pure-Go SQLite driver
// (glebarez/sqlite on modernc.org/sqlite). Every _pragma runs on each new
// connection, busy_timeout first so the remaining pragmas already wait on a
// locked database; _txlock=immediate makes BEGIN take the write lock up front.
func sqliteDSN(path string) string {
	return path + "?_pragma=busy_timeout(30000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=cache_size(-65536)" +
		"&_pragma=foreign_keys(1)" +
		"&_txlock=immediate"
}

// openSQLiteReadPool opens the query_only reader pool on the same database
// file. WAL is a persistent property of the file, so readers need no
// journal_mode pragma.
func openSQLiteReadPool(path string) (*gorm.DB, error) {
	dsn := path + "?_pragma=busy_timeout(30000)" +
		"&_pragma=cache_size(-65536)" +
		"&_pragma=query_only(1)"
	rdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite read pool: %w", err)
	}
	sqlDB, err := rdb.DB()
	if err != nil {
		return nil, fmt.Errorf("sqlite read pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(readPoolSize)
	sqlDB.SetMaxIdleConns(readPoolSize)
	sqlDB.SetConnMaxLifetime(0)
	return rdb, nil
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

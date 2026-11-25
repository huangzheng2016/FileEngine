// Package db owns the database handle: connection and dialect setup, schema
// migration, the typed query layer (package query) and the SQL helpers that
// differ per dialect. Repository and domain logic live in internal/db and
// biz/service.
package db

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"FileEngine/biz/dal/db/query"
	"FileEngine/internal/config"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	gdb    *gorm.DB
	q      *query.Query
	readQ  *query.Query
	readDB *gorm.DB // nil on MySQL/PostgreSQL, whose single pool already serves concurrent readers
)

// WAL lets readers run alongside the single writer connection.
const readPoolSize = 4

// DB is the writer handle; it is the only one on MySQL/PostgreSQL.
func DB() *gorm.DB { return gdb }

// Query is the typed query layer over DB.
func Query() *query.Query { return q }

// ReadDB is, on SQLite, a separate query_only pool so long reads never queue
// behind the single writer connection; elsewhere it is DB.
func ReadDB() *gorm.DB {
	if readDB != nil {
		return readDB
	}
	return gdb
}

// ReadQuery is Query over ReadDB.
func ReadQuery() *query.Query {
	if readQ != nil {
		return readQ
	}
	return q
}

// Init opens the configured database, migrates the schema and sets the
// package handles.
func Init(cfg config.DatabaseConfig) error {
	g, err := Open(cfg)
	if err != nil {
		return err
	}
	var rdb *gorm.DB
	if cfg.Driver == "sqlite" {
		// Opened after AutoMigrate: the file exists in WAL mode by now.
		if rdb, err = openSQLiteReadPool(cfg.DSN); err != nil {
			return err
		}
	}
	gdb, readDB = g, rdb
	q, readQ = query.Use(g), nil
	if rdb != nil {
		readQ = query.Use(rdb)
	}
	return nil
}

// Open connects and migrates without touching the package handles (dbtest uses
// it to get isolated databases).
func Open(cfg config.DatabaseConfig) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "sqlite":
		dialector = sqlite.Open(sqliteDSN(cfg.DSN))
	case "mysql":
		dialector = newMySQLDialector(cfg.DSN)
	case "postgres":
		dialector = postgres.Open(cfg.DSN)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	g, err := gorm.Open(dialector, &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect database: %w", err)
	}

	if cfg.Driver == "sqlite" {
		sqlDB, err := g.DB()
		if err != nil {
			return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
		}
		// SQLite allows one writer at a time; a single connection avoids lock
		// contention.
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(0)

		pragmas := []string{
			"PRAGMA temp_store=MEMORY",
			"PRAGMA mmap_size=268435456",
		}
		for _, p := range pragmas {
			if _, err := sqlDB.Exec(p); err != nil {
				return nil, fmt.Errorf("failed to set %s: %w", p, err)
			}
		}
	}

	if err := Migrate(g); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}
	return g, nil
}

// busy_timeout runs first so the remaining pragmas already wait on a locked
// database; _txlock=immediate makes BEGIN take the write lock up front.
// analysis_limit keeps the PRAGMA optimize after a scan of millions of rows
// to a sampled ANALYZE instead of a full one.
func sqliteDSN(path string) string {
	return path + "?_pragma=busy_timeout(30000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=cache_size(-65536)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=analysis_limit(1000)" +
		"&_txlock=immediate"
}

// WAL is a persistent property of the file, so readers need no journal_mode
// pragma.
func openSQLiteReadPool(path string) (*gorm.DB, error) {
	dsn := path + "?_pragma=busy_timeout(30000)" +
		"&_pragma=cache_size(-65536)" +
		"&_pragma=query_only(1)"
	rdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Warn),
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

// WithRetry re-runs fn while SQLite reports a locked database.
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
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "SQLITE_BUSY")
}

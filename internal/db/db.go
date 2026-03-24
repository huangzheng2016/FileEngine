package db

import (
	"fmt"

	"FileEngine/internal/config"

	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Init(cfg config.DatabaseConfig) error {
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "sqlite":
		dialector = sqlite.Open(cfg.DSN)
	case "mysql":
		dialector = mysql.Open(cfg.DSN)
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

	// SQLite performance optimizations
	if cfg.Driver == "sqlite" {
		sqlDB, err := DB.DB()
		if err != nil {
			return fmt.Errorf("failed to get underlying sql.DB: %w", err)
		}
		pragmas := []string{
			"PRAGMA journal_mode=WAL",
			"PRAGMA synchronous=NORMAL",
			"PRAGMA busy_timeout=5000",
			"PRAGMA cache_size=-20000", // 20MB cache
			"PRAGMA foreign_keys=ON",
			"PRAGMA temp_store=MEMORY",
		}
		for _, p := range pragmas {
			if _, err := sqlDB.Exec(p); err != nil {
				return fmt.Errorf("failed to set %s: %w", p, err)
			}
		}
	}

	if err := DB.AutoMigrate(&FileEntry{}, &Category{}, &Filesystem{}, &ScanSession{}, &AgentLog{}); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	return nil
}

func GetDB() *gorm.DB {
	return DB
}

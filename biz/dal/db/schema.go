package db

import (
	"fmt"
	"slices"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

var allModels = []any{&FileEntry{}, &Category{}, &Filesystem{}, &ScanSession{}, &ModelProvider{},
	&AgentRun{}, &SessionEvent{}, &SessionPlan{}, &MemoryItem{}, &ChatTurn{}, &PlanRule{}, &ExecOp{}}

// Creates the schema plus what struct tags cannot express: binary path
// comparison on MySQL and PostgreSQL.
func migrate(db *gorm.DB) error {
	tx := db
	if db.Dialector.Name() == "mysql" {
		opts, err := mysqlTableOptions(db)
		if err != nil {
			return err
		}
		tx = db.Set("gorm:table_options", opts)
	}
	if err := tx.AutoMigrate(allModels...); err != nil {
		return err
	}
	if db.Dialector.Name() == "postgres" {
		return postgresCollatePaths(db)
	}
	return nil
}

// mysqlTableOptions makes every table binary-collated so path ranges and
// ORDER BY compare bytes like SQLite; a NO PAD collation keeps trailing
// spaces significant where the server has one.
func mysqlTableOptions(db *gorm.DB) (string, error) {
	for _, c := range []string{"utf8mb4_0900_bin", "utf8mb4_nopad_bin", "utf8mb4_bin"} {
		var n int64
		if err := db.Raw("SELECT COUNT(*) FROM information_schema.COLLATIONS WHERE COLLATION_NAME = ?", c).Scan(&n).Error; err != nil {
			return "", err
		}
		if n > 0 {
			return "DEFAULT CHARSET=utf8mb4 COLLATE=" + c, nil
		}
	}
	return "", fmt.Errorf("mysql: no binary utf8mb4 collation")
}

// postgresPathColumns are compared as byte ranges (subtreeCond) and must
// carry "C" collation for their indexes to serve those ranges.
var postgresPathColumns = map[string][]string{
	"file_entries": {"original_path", "new_path", "new_path_key", "parent_path"},
	"categories":   {"path"},
}

func postgresCollatePaths(db *gorm.DB) error {
	for table, cols := range postgresPathColumns {
		for _, col := range cols {
			var coll *string
			if err := db.Raw(`SELECT collation_name FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`, table, col).
				Scan(&coll).Error; err != nil {
				return err
			}
			if coll != nil && *coll == "C" {
				continue
			}
			if err := db.Exec(fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s TYPE varchar(1024) COLLATE "C"`, table, col)).Error; err != nil {
				return fmt.Errorf("collate %s.%s: %w", table, col, err)
			}
		}
	}
	return nil
}

// mysqlMaxKeyBytes is InnoDB's index key limit (DYNAMIC row format).
const mysqlMaxKeyBytes = 3072

// mysqlDialector shortens long string columns in index definitions to
// prefixes that fit InnoDB's key limit at 4 bytes per utf8mb4 character.
// SQLite and PostgreSQL keep full-length indexes (GORM would otherwise emit
// the prefix syntax on every dialect).
type mysqlDialector struct{ *mysql.Dialector }

func newMySQLDialector(dsn string) gorm.Dialector {
	return mysqlDialector{mysql.Open(dsn).(*mysql.Dialector)}
}

func (d mysqlDialector) Migrator(db *gorm.DB) gorm.Migrator {
	return mysqlMigrator{d.Dialector.Migrator(db).(mysql.Migrator)}
}

type mysqlMigrator struct{ mysql.Migrator }

func (m mysqlMigrator) BuildIndexOptions(opts []schema.IndexOption, stmt *gorm.Statement) []any {
	return m.Migrator.BuildIndexOptions(mysqlIndexPrefixes(opts), stmt)
}

// mysqlIndexPrefixes assigns prefix lengths so the index key stays within
// mysqlMaxKeyBytes: non-string columns count 8 bytes, and the remaining
// character budget is shared out shortest column first.
func mysqlIndexPrefixes(opts []schema.IndexOption) []schema.IndexOption {
	out := slices.Clone(opts)
	var str []int
	fixed := 0
	for i, o := range out {
		if o.Field != nil && o.Field.DataType == schema.String && o.Field.Size > 0 {
			str = append(str, i)
		} else {
			fixed += 8
		}
	}
	budget := (mysqlMaxKeyBytes - fixed) / 4
	slices.SortFunc(str, func(a, b int) int { return out[a].Field.Size - out[b].Field.Size })
	for k, i := range str {
		n := min(out[i].Field.Size, budget/(len(str)-k))
		if n < out[i].Field.Size {
			out[i].Length = n
		}
		budget -= n
	}
	return out
}

func mysqlIndexBytes(opts []schema.IndexOption) int {
	total := 0
	for _, o := range mysqlIndexPrefixes(opts) {
		switch {
		case o.Field != nil && o.Field.DataType == schema.String && o.Field.Size > 0 && o.Length > 0:
			total += 4 * o.Length
		case o.Field != nil && o.Field.DataType == schema.String && o.Field.Size > 0:
			total += 4 * o.Field.Size
		default:
			total += 8
		}
	}
	return total
}

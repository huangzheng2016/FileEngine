package db

import (
	"strings"

	"gorm.io/gorm"
)

// Dialect helpers shared by SQLite / MySQL / PostgreSQL.
//
// Path columns compare as raw bytes on every dialect: SQLite's default BINARY
// collation, a binary table collation on MySQL (mysqlTableOptions) and "C"
// collation on PostgreSQL (postgresCollatePaths). Subtree matches are byte
// ranges (subtreeCond) so they are served by the path indexes; LIKE, which
// SQLite never serves from a BINARY index, is kept for user-facing substring
// search only, with '%'/'_' escaped by likeLiteral.

// likeEscapeClause is appended to every LIKE/ILIKE condition whose pattern
// came from likeLiteral. The escape character is '!' rather than the common
// backslash because a literal backslash needs dialect-specific quoting inside
// the ESCAPE clause itself (MySQL parses '\' as an escaped quote), while '!'
// is an ordinary character in SQLite, MySQL and PostgreSQL string literals.
const likeEscapeClause = " ESCAPE '!'"

var likeEscaper = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")

func likeLiteral(s string) string { return likeEscaper.Replace(s) }

// subtreeCond matches column values strictly under dir (dir + "/<anything>")
// as the byte range [dir+"/", dir+"0"): '0' is the byte right after '/', so
// the range is exactly the "dir/" prefix. dir "" matches every "/"-rooted
// value.
func subtreeCond(tx *gorm.DB, column, dir string) (string, []any) {
	c := pathCol(tx, column)
	return "(" + c + " >= ? AND " + c + " < ?)", []any{dir + "/", dir + "0"}
}

func atOrUnderCond(tx *gorm.DB, column, dir string) (string, []any) {
	sub, args := subtreeCond(tx, column, dir)
	return "(" + column + " = ? OR " + sub + ")", append([]any{dir}, args...)
}

// pathCol forces byte-order comparison of a path column on PostgreSQL; on the
// "C"-collated path columns it is a no-op relabel, so indexes still apply.
func pathCol(tx *gorm.DB, column string) string {
	if tx.Dialector.Name() == "postgres" {
		return column + ` COLLATE "C"`
	}
	return column
}

// concatExpr concatenates SQL expressions (MySQL parses || as OR).
func concatExpr(tx *gorm.DB, parts ...string) string {
	if tx.Dialector.Name() == "mysql" {
		return "CONCAT(" + strings.Join(parts, ", ") + ")"
	}
	return strings.Join(parts, " || ")
}

func nameSearchCondition(tx *gorm.DB) string { return ciLikeCondition(tx, "name") }

// ciLikeCondition is the case-insensitive LIKE condition on column for the
// dialect of tx; the pattern must come from likeLiteral. SQLite LIKE is
// already ASCII case-insensitive, PostgreSQL needs ILIKE, and MySQL tables use
// a binary collation, so the column is relabeled to a case-insensitive one.
func ciLikeCondition(tx *gorm.DB, column string) string {
	switch tx.Dialector.Name() {
	case "postgres":
		return column + " ILIKE ?" + likeEscapeClause
	case "mysql":
		return column + " COLLATE utf8mb4_general_ci LIKE ?" + likeEscapeClause
	}
	return column + " LIKE ?" + likeEscapeClause
}

// quoteCol quotes a column name for the dialect of tx — needed for raw
// conditions on columns named after reserved words (memory_items.key is
// reserved in MySQL).
func quoteCol(tx *gorm.DB, column string) string {
	var sb strings.Builder
	tx.Dialector.QuoteTo(&sb, column)
	return sb.String()
}

// instrFunc names the dialect's "1-based position of substring" function
// (instr(str, sub) on SQLite/MySQL, strpos(str, sub) on PostgreSQL). Paired
// with the portable substr() it lets path aggregations split off the next
// path segment in SQL.
func instrFunc(tx *gorm.DB) string {
	if tx.Dialector.Name() == "postgres" {
		return "strpos"
	}
	return "instr"
}

func entriesUnder(tx *gorm.DB, sessionID uint, dir string) *gorm.DB {
	cond, args := subtreeCond(tx, "original_path", dir)
	return tx.Model(&FileEntry{}).Where("scan_session_id = ?", sessionID).Where(cond, args...)
}

package db

import (
	"strings"

	"gorm.io/gorm"
)

// LIKE helpers shared by the three supported dialects (SQLite / MySQL /
// PostgreSQL). Two semantic differences matter here:
//
//   - Case sensitivity: PostgreSQL LIKE is case-sensitive while SQLite (ASCII)
//     and MySQL (default *_ci collation) LIKE are not. Path-prefix matching
//     compares machine-generated paths from the same scanner, so exact-case
//     matching is the intended semantics and plain LIKE is correct on all
//     dialects. User-facing name search must stay case-insensitive
//     everywhere, so PostgreSQL gets ILIKE (see nameSearchCondition).
//   - Wildcards: % and _ are LIKE metacharacters in every dialect, so a
//     literal path or search fragment containing them over-matches (e.g. a
//     "my_dir" prefix pattern also matches "myXdir"). likeLiteral escapes
//     them; patterns built with it must carry likeEscapeClause.

// likeEscapeClause is appended to every LIKE/ILIKE condition whose pattern
// came from likeLiteral. The escape character is '!' rather than the common
// backslash because a literal backslash needs dialect-specific quoting inside
// the ESCAPE clause itself (MySQL parses '\' as an escaped quote), while '!'
// is an ordinary character in SQLite, MySQL and PostgreSQL string literals.
const likeEscapeClause = " ESCAPE '!'"

var likeEscaper = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")

// likeLiteral escapes LIKE wildcards in s so it matches literally.
func likeLiteral(s string) string { return likeEscaper.Replace(s) }

// childPathPattern matches path-column values strictly under dir
// (dir + "/<anything>"), with LIKE wildcards in dir neutralized.
func childPathPattern(dir string) string { return likeLiteral(dir) + "/%" }

// nameSearchCondition returns the case-insensitive name-search condition for
// the dialect of tx: PostgreSQL needs ILIKE (its LIKE is case-sensitive),
// SQLite and MySQL LIKE are already case-insensitive.
func nameSearchCondition(tx *gorm.DB) string {
	if tx.Dialector.Name() == "postgres" {
		return "name ILIKE ?" + likeEscapeClause
	}
	return "name LIKE ?" + likeEscapeClause
}

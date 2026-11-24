package db

import (
	"FileEngine/biz/dal/db/query"

	"gorm.io/gorm"
)

// Reader is the typed query layer for reads that need no read-your-writes
// inside one request. On SQLite it runs on the query_only pool, so long reads
// never queue behind the single writer connection; elsewhere it is the writer
// handle. A separate type so Wire can tell it from the writer's *query.Query.
type Reader struct {
	*query.Query
	// DB is the handle the queries run on, for the repository's reads.
	DB *gorm.DB
}

// NewReader builds the read layer over rdb.
func NewReader(rdb *gorm.DB) *Reader { return &Reader{Query: query.Use(rdb), DB: rdb} }

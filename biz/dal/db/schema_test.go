package db

import (
	"sync"
	"testing"

	"FileEngine/biz/dal/db/model"

	"gorm.io/gorm/schema"
)

// Every index must fit InnoDB's 3072-byte key at 4 bytes per utf8mb4 char
// once mysqlIndexPrefixes has applied its prefixes, and no indexed string
// column may lack a size (MySQL cannot index unbounded TEXT).
func TestMySQLIndexWidths(t *testing.T) {
	cache := &sync.Map{}
	for _, m := range model.All() {
		s, err := schema.Parse(m, cache, schema.NamingStrategy{})
		if err != nil {
			t.Fatal(err)
		}
		for _, idx := range s.ParseIndexes() {
			for _, o := range idx.Fields {
				if o.Field.DataType == schema.String && o.Field.Size == 0 {
					t.Errorf("%s.%s: indexed string column %s has no size", s.Table, idx.Name, o.Field.DBName)
				}
			}
			if n := mysqlIndexBytes(idx.Fields); n > mysqlMaxKeyBytes {
				t.Errorf("%s.%s: %d bytes", s.Table, idx.Name, n)
			}
		}
	}
	s, _ := schema.Parse(&model.FileEntry{}, cache, schema.NamingStrategy{})
	for _, idx := range s.ParseIndexes() {
		if idx.Name == "idx_uniq_session_path" {
			opts := mysqlIndexPrefixes(idx.Fields)
			if opts[0].Length != 0 || opts[1].Length != 766 {
				t.Fatalf("prefixes: %d %d", opts[0].Length, opts[1].Length)
			}
		}
	}
}

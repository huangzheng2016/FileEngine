// Package container holds the process-wide services. Handlers and middleware
// reach them through Default; tests build services directly or call Init.
package container

// go generate skips files excluded by build tags, so wire.go's directive lives here.
//
//go:generate go tool wire

import (
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/service/auth"
	"FileEngine/biz/service/category"
	"FileEngine/biz/service/chat"
	"FileEngine/biz/service/eventfeed"
	"FileEngine/biz/service/file"
	"FileEngine/biz/service/memory"
	"FileEngine/biz/service/plan"
	"FileEngine/biz/service/rule"
	"FileEngine/biz/service/runner"
	"FileEngine/internal/db"

	"gorm.io/gorm"
)

type Container struct {
	// DB is the writer handle; Repo wraps it for every query the old handlers made.
	DB   *gorm.DB
	Repo *db.Repository
	Auth *auth.Service
	// Runner launches and stops runs and scans, and drains them at shutdown.
	Runner *runner.Service
	// Streams owns every open event stream; the app closes it first at shutdown.
	Streams *eventfeed.Registry

	// Query is the typed query layer over the writer handle.
	Query *query.Query
	// Resource services: files, categories, plan, rules, memory, chat.
	Files      *file.Service
	Categories *category.Service
	Plans      *plan.Service
	Rules      *rule.Service
	Memory     *memory.Service
	Chat       *chat.Service
}

func newQuery(gdb *gorm.DB) *query.Query { return query.Use(gdb) }

var Default *Container

// Init builds the container over an opened database and installs it as Default.
func Init(gdb *gorm.DB, authOpts auth.Options) error {
	c, err := build(gdb, authOpts)
	if err != nil {
		return err
	}
	Default = c
	return nil
}

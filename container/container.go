// Package container holds the process-wide services. Handlers and middleware
// reach them through Default; tests build services directly or call Init.
package container

// go generate skips files excluded by build tags, so wire.go's directive lives here.
//
//go:generate go tool wire

import (
	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/query"
	"FileEngine/biz/service/admin"
	"FileEngine/biz/service/auth"
	"FileEngine/biz/service/category"
	"FileEngine/biz/service/chat"
	"FileEngine/biz/service/event"
	"FileEngine/biz/service/eventfeed"
	"FileEngine/biz/service/execute"
	"FileEngine/biz/service/file"
	"FileEngine/biz/service/filesystem"
	"FileEngine/biz/service/memory"
	"FileEngine/biz/service/modelprovider"
	"FileEngine/biz/service/plan"
	"FileEngine/biz/service/rule"
	"FileEngine/biz/service/run"
	"FileEngine/biz/service/runner"
	"FileEngine/biz/service/session"
	"FileEngine/biz/service/settings"
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

	// Storages, models, runtime settings, operator actions.
	Filesystems *filesystem.Service
	Models      *modelprovider.Service
	Settings    *settings.Service
	Admin       *admin.Service

	// Query is the typed query layer over the writer handle.
	Query *query.Query
	// Read is the typed query layer for reads that need no read-your-writes.
	Read *dal.Reader
	// Resource services: files, categories, plan, rules, memory, chat.
	Files      *file.Service
	Categories *category.Service
	Plans      *plan.Service
	Rules      *rule.Service
	Memory     *memory.Service
	Chat       *chat.Service

	// Sessions, runs, execution and events.
	Session *session.Service
	Run     *run.Service
	Execute *execute.Service
	Event   *event.Service
}

var Default *Container

// newQuery is the typed query layer of the writer handle; wire cannot inject
// query.Use's variadic options.
func newQuery(gdb *gorm.DB) *query.Query { return query.Use(gdb) }

// Init builds the container over an opened database and installs it as Default.
// rdb is the read-only pool (nil: reads use gdb).
func Init(gdb, rdb *gorm.DB, authOpts auth.Options) error {
	if rdb == nil {
		rdb = gdb
	}
	c, err := build(gdb, dal.NewReader(rdb), authOpts)
	if err != nil {
		return err
	}
	Default = c
	return nil
}

//go:build wireinject

package container

import (
	"FileEngine/biz/service/auth"
	"FileEngine/biz/service/event"
	"FileEngine/biz/service/eventfeed"
	"FileEngine/biz/service/execute"
	"FileEngine/biz/service/run"
	"FileEngine/biz/service/runner"
	"FileEngine/biz/service/session"
	"FileEngine/internal/db"

	"github.com/google/wire"
	"gorm.io/gorm"
)

// A new service: add its constructor here and a field to Container, then
// run go generate ./container.
func build(gdb *gorm.DB, authOpts auth.Options) (*Container, error) {
	wire.Build(
		db.NewRepository,
		auth.NewService,
		runner.NewService,
		eventfeed.NewRegistry,
		newQuery,
		session.NewService,
		run.NewService,
		execute.NewService,
		event.NewService,
		wire.Struct(new(Container), "*"),
	)
	return nil, nil
}

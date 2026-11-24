//go:build wireinject

package container

import (
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
	"FileEngine/biz/service/sessionagent"
	"FileEngine/biz/service/settings"
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
		filesystem.NewService,
		modelprovider.NewService,
		settings.NewService,
		admin.NewService,
		sessionagent.NewFactory,
		file.NewService,
		category.NewService,
		plan.NewService,
		rule.NewService,
		memory.NewService,
		chat.NewService,
		session.NewService,
		run.NewService,
		execute.NewService,
		event.NewService,
		wire.Struct(new(Container), "*"),
	)
	return nil, nil
}

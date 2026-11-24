// Command generate regenerates biz/dal/db/query from the structs in biz/dal/db/model.
package main

import (
	"FileEngine/biz/dal/db/model"

	"gorm.io/gen"
)

func main() {
	g := gen.NewGenerator(gen.Config{
		OutPath:      "biz/dal/db/query",
		ModelPkgPath: "biz/dal/db/model",
	})
	g.ApplyBasic(model.All()...)
	g.Execute()
}

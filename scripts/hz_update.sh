#!/bin/bash
# Regenerates biz/router, biz/model and new handler stubs from idl/. Existing
# handler bodies and biz/router/*/middleware.go are preserved. Needs the pinned
# generators on PATH:
#   go install github.com/cloudwego/hertz/cmd/hz@v0.9.7
#   go install github.com/cloudwego/thriftgo@v0.4.1
set -euo pipefail
cd "$(dirname "$0")/.."
hz update -idl idl/api.thrift -handler_dir biz/handler -model_dir biz/model
# hz re-adds every model import to the handler files; drop the ones a handler
# does not use, then format (the format gate covers biz/).
go tool goimports -w biz/handler
"$(go env GOROOT)/bin/gofmt" -w biz

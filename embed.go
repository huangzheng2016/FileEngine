package main

import "embed"

//go:embed all:web/dist
var frontendFS embed.FS

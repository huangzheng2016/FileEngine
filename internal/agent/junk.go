package agent

import "FileEngine/internal/db"

// The canonical junk name lists live in the db package (db.JunkFileNames /
// db.JunkDirNames) so the scanner's traversal-time pruning and this layer's
// post-scan auto-ignore share one source of truth. This file keeps the
// agent-local aliases; matching is ASCII-case-insensitive on the base name,
// and AppleDouble sidecars ("._*") are always junk on NAS shares.
var junkFileNames = db.JunkFileNames

var junkDirNames = db.JunkDirNames

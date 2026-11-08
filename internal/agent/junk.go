package agent

import "FileEngine/internal/db"

// Matching is ASCII-case-insensitive on the base name; AppleDouble sidecars
// ("._*") are always junk on NAS shares.
var junkFileNames = db.JunkFileNames

var junkDirNames = db.JunkDirNames

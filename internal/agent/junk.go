package agent

// Well-known system junk that should never reach an agent batch: it carries
// no classification signal, wastes tokens, and pollutes the uncategorized
// bucket. Matching is on the entry's base name, ASCII-case-insensitive;
// AppleDouble sidecars ("._*") are always junk on NAS shares.
var junkFileNames = []string{
	".ds_store",
	"thumbs.db",
	"desktop.ini",
}

var junkDirNames = []string{
	"@eadir",
	"__macosx",
	"$recycle.bin",
	".trashes",
	".spotlight-v100",
	".fseventsd",
	".temporaryitems",
}

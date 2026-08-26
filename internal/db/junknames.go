package db

// Well-known system junk that should never reach an agent batch: it carries
// no classification signal, wastes tokens, and pollutes the uncategorized
// bucket. Matching is on the entry's base name, ASCII-case-insensitive;
// AppleDouble sidecars ("._*") are always junk on NAS shares.
//
// Two filter layers consume these names, with different granularity:
//   - scanner (traversal-time): skips JunkDirNames outright — those
//     directories are never listed and never reach the DB;
//   - agent (post-scan, MarkJunkEntries): marks JunkFileNames, "._*" files
//     and any surviving junk dirs tagged so no tokens are spent on them.
var JunkFileNames = []string{
	".ds_store",
	"thumbs.db",
	"desktop.ini",
}

var JunkDirNames = []string{
	"@eadir",
	"__macosx",
	"$recycle.bin",
	".trashes",
	".spotlight-v100",
	".fseventsd",
	".temporaryitems",
}

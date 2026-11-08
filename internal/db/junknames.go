package db

// The scanner skips JunkDirNames outright (never listed, never stored); the
// agent pre-pass marks JunkFileNames, "._*" files and any surviving junk dirs
// so no tokens are spent on them. Matching is ASCII-case-insensitive.
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

package db

import (
	"path/filepath"
	"testing"

	"FileEngine/internal/config"
)

// MarkJunkEntries must auto-ignore junk files (incl. AppleDouble sidecars) and
// junk directories with their whole subtree, leaving real content untouched.
func TestMarkJunkEntries(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(DB)

	entries := []FileEntry{
		{ScanSessionID: 1, OriginalPath: "p/.DS_Store", Name: ".DS_Store", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "p/._photo.jpg", Name: "._photo.jpg", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "p/Thumbs.db", Name: "Thumbs.db", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "p/photo.jpg", Name: "photo.jpg", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "p/@eaDir", Name: "@eaDir", FileType: "directory"},
		{ScanSessionID: 1, OriginalPath: "p/@eaDir/thumb.jpg", Name: "thumb.jpg", FileType: "file"},
		{ScanSessionID: 1, OriginalPath: "p/docs", Name: "docs", FileType: "directory"},
	}
	for i := range entries {
		if err := r.CreateFile(&entries[i]); err != nil {
			t.Fatalf("seed %s: %v", entries[i].OriginalPath, err)
		}
	}

	n, err := r.MarkJunkEntries(1, []string{".ds_store", "thumbs.db", "desktop.ini"}, []string{"@eadir", "__macosx"})
	if err != nil {
		t.Fatalf("MarkJunkEntries: %v", err)
	}
	if n != 5 {
		t.Errorf("ignored %d rows, want 5 (.DS_Store, ._photo.jpg, Thumbs.db, @eaDir + child)", n)
	}

	mustState := func(path string, tagged bool) {
		t.Helper()
		f, err := r.GetFileByPath(1, path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		if f.Tagged != tagged {
			t.Errorf("%s tagged=%v, want %v", path, f.Tagged, tagged)
		}
	}
	mustState("p/.DS_Store", true)
	mustState("p/._photo.jpg", true)
	mustState("p/Thumbs.db", true)
	mustState("p/@eaDir", true)
	mustState("p/@eaDir/thumb.jpg", true)
	mustState("p/photo.jpg", false)
	mustState("p/docs", false)

	ignored, err := r.CountIgnoredJunk(1)
	if err != nil {
		t.Fatalf("CountIgnoredJunk: %v", err)
	}
	if ignored != 5 {
		t.Errorf("CountIgnoredJunk = %d, want 5", ignored)
	}

	// Idempotent: a second pass ignores nothing new.
	if n, err := r.MarkJunkEntries(1, []string{".ds_store"}, []string{"@eadir"}); err != nil || n != 0 {
		t.Errorf("second pass: n=%d err=%v, want 0/nil", n, err)
	}
}

// IDMod sharding splits a result set deterministically and disjointly.
func TestListFilesIDModSharding(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := Init(config.DatabaseConfig{Driver: "sqlite", DSN: dsn}); err != nil {
		t.Fatalf("init: %v", err)
	}
	r := NewRepository(DB)
	for _, p := range []string{"a/1", "a/2", "a/3", "a/4", "a/5"} {
		fe := &FileEntry{ScanSessionID: 1, OriginalPath: p, Name: p, FileType: "file"}
		if err := r.CreateFile(fe); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	falseVal := false
	var totals [2]int64
	seen := map[uint]bool{}
	for rem := 0; rem < 2; rem++ {
		files, total, err := r.ListFiles(FileQuery{SessionID: 1, Tagged: &falseVal, Page: 1, PageSize: 10, IDMod: 2, IDModRem: rem})
		if err != nil {
			t.Fatalf("shard %d: %v", rem, err)
		}
		totals[rem] = total
		for _, f := range files {
			if seen[f.ID] {
				t.Fatalf("row %d in both shards", f.ID)
			}
			seen[f.ID] = true
			if int(f.ID%2) != rem {
				t.Fatalf("row %d in wrong shard %d", f.ID, rem)
			}
		}
	}
	if totals[0]+totals[1] != 5 {
		t.Fatalf("shards cover %d rows, want 5", totals[0]+totals[1])
	}
}

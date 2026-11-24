package db

import (
	"path/filepath"
	"testing"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
)

func seedRepo(t *testing.T, entries ...model.FileEntry) *Repository {
	t.Helper()
	if err := Open(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "test.db")}); err != nil {
		t.Fatal(err)
	}
	r := NewRepository(dal.DB())
	for i := range entries {
		entries[i].ScanSessionID = 1
		if err := r.DB().Create(&entries[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestJunkDirOverridesDecidedChildWithReason(t *testing.T) {
	r := seedRepo(t,
		model.FileEntry{OriginalPath: "@eaDir", Name: "@eaDir", FileType: "directory"},
		model.FileEntry{OriginalPath: "@eaDir/keep.jpg", Name: "keep.jpg", FileType: "file", ParentPath: "@eaDir"},
	)
	if _, err := r.Decide(1, Decision{Path: "@eaDir/keep.jpg", Role: RoleFile, NewPath: "/x/keep.jpg", Reason: "t"}); err != nil {
		t.Fatal(err)
	}
	n, err := r.MarkJunkEntries(1, nil, []string{"@eadir"})
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	child, _ := r.GetFileByPath(1, "@eaDir/keep.jpg")
	if child.Role != RoleMember || child.NewPath != "" || child.Operation != "" || child.Reason == "" || child.BatchIndex != JunkBatchIndex {
		t.Fatalf("decided child must become a member of the skipped folder: %+v", child)
	}
	dir, _ := r.GetFileByPath(1, "@eaDir")
	if dir.Role != RoleSkip || dir.Reason != "系统垃圾目录" || dir.BatchIndex != JunkBatchIndex {
		t.Fatalf("junk dir: %+v", dir)
	}
}

func TestDuplicateRoleOnlyForUndecidedFiles(t *testing.T) {
	r := seedRepo(t,
		model.FileEntry{OriginalPath: "a.bin", Name: "a.bin", FileType: "file"},
		model.FileEntry{OriginalPath: "b.bin", Name: "b.bin", FileType: "file"},
		model.FileEntry{OriginalPath: "u", Name: "u", FileType: "directory"},
		model.FileEntry{OriginalPath: "u/c.bin", Name: "c.bin", FileType: "file", ParentPath: "u"},
	)
	if _, err := r.Decide(1, Decision{Path: "b.bin", Role: RoleFile, NewPath: "/moved/b.bin", Reason: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Decide(1, Decision{Path: "u", Role: RoleUnit, NewPath: "/u", Reason: "t"}); err != nil {
		t.Fatal(err)
	}
	outs, err := r.DecideBatch(1, []Decision{
		{Path: "a.bin", Role: RoleDuplicate, Reason: "内容重复"},
		{Path: "b.bin", Role: RoleDuplicate, Reason: "内容重复"},
		{Path: "u/c.bin", Role: RoleDuplicate, Reason: "内容重复"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].Err != nil || outs[1].Err == nil || outs[2].Err == nil {
		t.Fatalf("only the undecided free file may become a duplicate: %+v", outs)
	}
	a, _ := r.GetFileByPath(1, "a.bin")
	b, _ := r.GetFileByPath(1, "b.bin")
	if a.Role != RoleDuplicate || a.Reason == "" || a.Tagged || b.Role != RoleFile {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}

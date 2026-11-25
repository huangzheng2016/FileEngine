package db

import (
	"fmt"
	"strings"
	"testing"

	"FileEngine/biz/dal/db/model"

	"gorm.io/gorm"
)

func TestFilesBySizeAfter_SharedSizesInKeysetOrder(t *testing.T) {
	r := setupScanProgressRepo(t)
	sess := &model.ScanSession{Status: "scanning"}
	if err := r.DB().Create(sess).Error; err != nil {
		t.Fatal(err)
	}
	other := &model.ScanSession{Status: "scanning"}
	if err := r.DB().Create(other).Error; err != nil {
		t.Fatal(err)
	}
	add := func(sid uint, p, typ string, size int64) {
		t.Helper()
		if err := r.DB().Create(&model.FileEntry{ScanSessionID: sid, OriginalPath: p, Name: p, FileType: typ, Size: size}).Error; err != nil {
			t.Fatal(err)
		}
	}
	add(sess.ID, "c1", "file", 30)
	add(sess.ID, "a1", "file", 10)
	add(sess.ID, "lonely", "file", 20)
	add(sess.ID, "a2", "file", 10)
	add(sess.ID, "c2", "file", 30)
	add(sess.ID, "c3", "file", 30)
	add(sess.ID, "dir", "directory", 20) // a directory never pairs with a file
	add(sess.ID, "e1", "file", 0)        // empty files are never duplicates
	add(sess.ID, "e2", "file", 0)
	add(other.ID, "x", "file", 20) // nor does another session's file

	var got []string
	var afterSize int64
	var afterID uint
	for {
		rows, err := r.FilesBySizeAfter(sess.ID, afterSize, afterID, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			got = append(got, fmt.Sprintf("%s:%d", row.OriginalPath, row.Size))
		}
		if len(rows) < 2 {
			break
		}
		afterSize, afterID = rows[len(rows)-1].Size, rows[len(rows)-1].ID
	}
	if want := "a1:10 a2:10 c1:30 c2:30 c3:30"; strings.Join(got, " ") != want {
		t.Errorf("stream = %q, want %q", strings.Join(got, " "), want)
	}
	total, err := r.DuplicateCandidateBytes(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if total != 110 {
		t.Errorf("candidate bytes = %d, want 110", total)
	}
}

// The stream runs once per page over millions of rows: both the scan and the
// sibling probe must be index searches, never a table scan or a sort.
func TestFilesBySizeAfter_UsesSizeIndex(t *testing.T) {
	r := setupScanProgressRepo(t)
	var plan []struct {
		Detail string
	}
	q := r.Reader().ToSQL(func(tx *gorm.DB) *gorm.DB {
		var rows []DupCandidate
		return filesBySize(tx, 1, 5, 7, 10).Find(&rows)
	})
	if err := r.Reader().Raw("EXPLAIN QUERY PLAN " + q).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, p := range plan {
		lines = append(lines, p.Detail)
	}
	text := strings.Join(lines, "\n")
	if strings.Contains(text, "SCAN") || strings.Contains(text, "TEMP B-TREE") {
		t.Errorf("plan:\n%s", text)
	}
	if strings.Count(text, "idx_session_type_size") < 2 {
		t.Errorf("plan does not use idx_session_type_size twice:\n%s", text)
	}
}

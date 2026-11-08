package agent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"FileEngine/internal/db"
)

func makeZip(t *testing.T, names []string) ([]byte, int64) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		if strings.HasSuffix(n, "/") {
			if _, err := zw.Create(n); err != nil {
				t.Fatal(err)
			}
			continue
		}
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), int64(buf.Len())
}

func TestZipHint(t *testing.T) {
	data, size := makeZip(t, []string{
		"proj/", "proj/main.go", "proj/go.mod", "proj/README.md", "proj/tools/a.py",
		"__MACOSX/", "__MACOSX/proj/._main.go",
	})
	hint, err := zipHint(bytes.NewReader(data), size, 2)
	if err != nil {
		t.Fatalf("zipHint: %v", err)
	}
	if !strings.Contains(hint, "zip 压缩包，共 5 个条目") {
		t.Errorf("total must count real entries incl. dirs, excl. __MACOSX:\n%s", hint)
	}
	if strings.Contains(hint, "__MACOSX") {
		t.Errorf("noise leaked:\n%s", hint)
	}
	if !strings.Contains(hint, "proj/main.go") || !strings.Contains(hint, "proj/go.mod") {
		t.Errorf("first names missing:\n%s", hint)
	}
	if strings.Contains(hint, "README") {
		t.Errorf("maxNames=2 must bound the name list:\n%s", hint)
	}
}

func TestTarHint(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, n := range []string{"site/", "site/index.html", "site/app.js", "site/style.css"} {
		if strings.HasSuffix(n, "/") {
			if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0644, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	hint, err := tarHint(bytes.NewReader(buf.Bytes()), true, 3)
	if err != nil {
		t.Fatalf("tarHint: %v", err)
	}
	if !strings.Contains(hint, "tar.gz 压缩包，共 4 个条目") {
		t.Errorf("count wrong:\n%s", hint)
	}
	if !strings.Contains(hint, "site/index.html") {
		t.Errorf("names missing:\n%s", hint)
	}
}

func makeBatchEntries(t *testing.T) []db.FileEntry {
	t.Helper()
	return []db.FileEntry{
		{ID: 1, ScanSessionID: 1, OriginalPath: "p/live-vision.zip", Name: "live-vision.zip", FileType: "file", Size: 1024},
		{ID: 2, ScanSessionID: 1, OriginalPath: "p/readme.txt", Name: "readme.txt", FileType: "file", Size: 128},
	}
}

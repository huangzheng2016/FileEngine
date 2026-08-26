package agent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

// buildTestPDF assembles a minimal but well-formed single-font PDF (correct
// xref offsets) whose pages each show one line of ASCII text.
func buildTestPDF(t *testing.T, pages ...string) []byte {
	t.Helper()
	escape := func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `(`, `\(`)
		s = strings.ReplaceAll(s, `)`, `\)`)
		return s
	}

	// Object layout: 1=catalog, 2=pages, then per page (page, content), font last.
	fontNum := 3 + 2*len(pages)
	objects := map[int]string{}
	objects[1] = "<< /Type /Catalog /Pages 2 0 R >>"
	var kids []string
	for i, text := range pages {
		pageNum := 3 + 2*i
		contentNum := pageNum + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", pageNum))
		objects[pageNum] = fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", fontNum, contentNum)
		stream := fmt.Sprintf("BT /F1 24 Tf 72 720 Td (%s) Tj ET", escape(text))
		objects[contentNum] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)
	}
	objects[2] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages))
	objects[fontNum] = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make(map[int]int, fontNum)
	for num := 1; num <= fontNum; num++ {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, objects[num])
	}
	xrefPos := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", fontNum+1)
	buf.WriteString("0000000000 65535 f \n")
	for num := 1; num <= fontNum; num++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[num])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", fontNum+1, xrefPos)
	return buf.Bytes()
}

// buildTestZip packs the given members (name → content) into a zip archive.
func buildTestZip(t *testing.T, members map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// newReadFileToolBuilder wires a ToolBuilder against a local temp dir.
func newReadFileToolBuilder(t *testing.T, dir string, cfg config.AgentConfig) *ToolBuilder {
	t.Helper()
	fs, err := remotefs.NewLocalFS(dir)
	if err != nil {
		t.Fatalf("NewLocalFS: %v", err)
	}
	return NewToolBuilder(nil, fs, 1, 1, NewLogger(nil, 1, 0), cfg, &db.ScanSession{AllowReadFile: true}, false)
}

func writeTestFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// read_file extracts text from PDF pages instead of reporting a binary blob.
func TestReadFilePDFExtraction(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "report.pdf", buildTestPDF(t, "Hello PDF page one", "Second page text"))

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{})
	out, err := tb.readFile(context.Background(), &ReadFileInput{Path: "report.pdf"})
	if err != nil {
		t.Fatalf("readFile pdf: %v", err)
	}
	for _, want := range []string{"Hello PDF page one", "Second page text"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("pdf text missing %q:\n%s", want, out.Content)
		}
	}
	if out.Truncated {
		t.Fatalf("small pdf must not be truncated: %+v", out)
	}
}

// A PDF whose text exceeds the character cap is cut and flagged truncated.
func TestReadFilePDFCharCapTruncation(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "long.pdf", buildTestPDF(t, "abcdefghij", "klmnopqrst"))

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{MaxFileReadSize: 15})
	out, err := tb.readFile(context.Background(), &ReadFileInput{Path: "long.pdf"})
	if err != nil {
		t.Fatalf("readFile pdf: %v", err)
	}
	if !out.Truncated {
		t.Fatalf("capped pdf must be truncated: %+v", out)
	}
	if len(out.Content) > 15 {
		t.Fatalf("content over the rune cap: %d chars: %q", len(out.Content), out.Content)
	}
}

// docx paragraphs come out as text lines via the zip+xml stdlib path.
func TestReadFileDocxExtraction(t *testing.T) {
	dir := t.TempDir()
	docx := buildTestZip(t, map[string]string{
		"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			`<w:p><w:r><w:t>Hello docx</w:t></w:r></w:p>` +
			`<w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p>` +
			`</w:body></w:document>`,
	})
	writeTestFile(t, dir, "notes.docx", docx)

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{})
	out, err := tb.readFile(context.Background(), &ReadFileInput{Path: "notes.docx"})
	if err != nil {
		t.Fatalf("readFile docx: %v", err)
	}
	if !strings.Contains(out.Content, "Hello docx\n") || !strings.Contains(out.Content, "Second paragraph") {
		t.Fatalf("docx text wrong:\n%s", out.Content)
	}
}

// xlsx worksheets render as TSV with shared strings resolved and inline
// strings used directly; .rels members must not be picked up as sheets.
func TestReadFileXlsxExtraction(t *testing.T) {
	dir := t.TempDir()
	xlsx := buildTestZip(t, map[string]string{
		"xl/sharedStrings.xml": `<sst><si><t>Alpha</t></si><si><t>Beta</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>` +
			`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1"><v>42</v></c></row>` +
			`<row r="2"><c r="A2" t="s"><v>1</v></c><c r="B2" t="inlineStr"><is><t>inline</t></is></c></row>` +
			`</sheetData></worksheet>`,
		"xl/worksheets/_rels/sheet1.xml.rels": `<Relationships></Relationships>`,
	})
	writeTestFile(t, dir, "table.xlsx", xlsx)

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{})
	out, err := tb.readFile(context.Background(), &ReadFileInput{Path: "table.xlsx"})
	if err != nil {
		t.Fatalf("readFile xlsx: %v", err)
	}
	if !strings.Contains(out.Content, "Alpha\t42\n") || !strings.Contains(out.Content, "Beta\tinline") {
		t.Fatalf("xlsx text wrong:\n%s", out.Content)
	}
}

// pptx slides contribute their text runs in slide order.
func TestReadFilePptxExtraction(t *testing.T) {
	dir := t.TempDir()
	pptx := buildTestZip(t, map[string]string{
		"ppt/slides/slide1.xml": `<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody>` +
			`<a:p><a:r><a:t>First slide title</a:t></a:r></a:p>` +
			`</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`,
		"ppt/slides/slide2.xml": `<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody>` +
			`<a:p><a:r><a:t>Second slide</a:t></a:r></a:p>` +
			`</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`,
	})
	writeTestFile(t, dir, "deck.pptx", pptx)

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{})
	out, err := tb.readFile(context.Background(), &ReadFileInput{Path: "deck.pptx"})
	if err != nil {
		t.Fatalf("readFile pptx: %v", err)
	}
	first := strings.Index(out.Content, "First slide title")
	second := strings.Index(out.Content, "Second slide")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("pptx slides missing or out of order:\n%s", out.Content)
	}
}

// The legacy binary .doc format is refused with a clear unsupported note,
// never parsed and never mistaken for a generic binary blob.
func TestReadFileDocUnsupported(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "old.doc", []byte{0xD0, 0xCF, 0x11, 0xE0, 0x00, 0x01})

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{})
	out, err := tb.readFile(context.Background(), &ReadFileInput{Path: "old.doc"})
	if err != nil {
		t.Fatalf("readFile .doc must not error: %v", err)
	}
	if !strings.Contains(out.Content, ".doc") || !strings.Contains(out.Content, "not supported") && !strings.Contains(out.Content, "unsupported") {
		t.Fatalf("unsupported note missing: %q", out.Content)
	}
	if strings.Contains(out.Content, "binary file") {
		t.Fatalf(".doc must get the format note, not the generic binary note: %q", out.Content)
	}
}

// Plain-text behavior is unchanged: byte cap + truncated flag, binary sniff,
// and a corrupt document returns a loud extraction error.
func TestReadFileTextAndErrorPaths(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "big.txt", []byte(strings.Repeat("a", 200)))
	writeTestFile(t, dir, "broken.pdf", []byte("this is not a pdf"))
	writeTestFile(t, dir, "blob.bin", append([]byte("abc"), 0x00, 0x01))

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{MaxFileReadSize: 100})

	out, err := tb.readFile(context.Background(), &ReadFileInput{Path: "big.txt"})
	if err != nil {
		t.Fatalf("readFile txt: %v", err)
	}
	if !out.Truncated || len(out.Content) != 100 {
		t.Fatalf("text truncation: got truncated=%v len=%d", out.Truncated, len(out.Content))
	}

	out, err = tb.readFile(context.Background(), &ReadFileInput{Path: "blob.bin"})
	if err != nil {
		t.Fatalf("readFile bin: %v", err)
	}
	if !strings.Contains(out.Content, "binary file") {
		t.Fatalf("binary note missing: %q", out.Content)
	}

	if _, err := tb.readFile(context.Background(), &ReadFileInput{Path: "broken.pdf"}); err == nil {
		t.Fatal("corrupt pdf must return an extraction error")
	}

	if _, err := tb.readFile(context.Background(), &ReadFileInput{Path: "../escape.pdf"}); err == nil {
		t.Fatal("path escape must be rejected")
	}
}

// ============ Archive support ============

// streamOnlyFS hides any ReaderAt/Seeker the wrapped backend's files have,
// simulating an FTP-style stream-only backend.
type streamOnlyFS struct{ remotefs.RemoteFS }

func (s streamOnlyFS) ReadFile(ctx context.Context, p string) (io.ReadCloser, error) {
	rc, err := s.RemoteFS.ReadFile(ctx, p)
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{rc, rc}, nil
}

// seekOnlyFS hides ReaderAt but keeps Seek, simulating the NFS backend
// (*nfs.File: Seek without ReadAt).
type seekOnlyFS struct{ remotefs.RemoteFS }

func (s seekOnlyFS) ReadFile(ctx context.Context, p string) (io.ReadCloser, error) {
	rc, err := s.RemoteFS.ReadFile(ctx, p)
	if err != nil {
		return nil, err
	}
	sk := rc.(io.Seeker)
	return struct {
		io.Reader
		io.Seeker
		io.Closer
	}{rc, sk, rc}, nil
}

// buildOversizedZip builds a valid zip whose single stored entry pushes the
// archive past the 32MB document cap.
func buildOversizedZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "big.bin", Method: zip.Store})
	if err != nil {
		t.Fatalf("zip create header: %v", err)
	}
	if _, err := w.Write(make([]byte, maxDocumentReadBytes+1)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// buildTar packs entries (name → content) into a tar, optionally gzipped.
func buildTar(t *testing.T, gz bool, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.Writer = &buf
	var gzw *gzip.Writer
	if gz {
		gzw = gzip.NewWriter(&buf)
		w = gzw
	}
	tw := tar.NewWriter(w)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if gzw != nil {
		if err := gzw.Close(); err != nil {
			t.Fatalf("gzip close: %v", err)
		}
	}
	return buf.Bytes()
}

// zip listing shows entry names/sizes with a read hint; the entry list is
// capped with a truncation trailer.
func TestReadFileZipListing(t *testing.T) {
	dir := t.TempDir()
	z := buildTestZip(t, map[string]string{
		"docs/a.txt": "alpha content",
		"docs/b.txt": "beta content",
		"README.md":  "# readme",
	})
	writeTestFile(t, dir, "bundle.zip", z)

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{})
	out, err := tb.readFile(context.Background(), &ReadFileInput{Path: "bundle.zip"})
	if err != nil {
		t.Fatalf("readFile zip list: %v", err)
	}
	for _, want := range []string{"zip archive: 3 entries", "docs/a.txt", "docs/b.txt", "README.md", "::"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("zip listing missing %q:\n%s", want, out.Content)
		}
	}
	if out.Truncated {
		t.Fatalf("small listing must not be truncated: %+v", out)
	}
}

// Entry reads via the path::entry syntax: small text entries come back as
// content; oversized entries and missing entries are refused explicitly.
func TestReadFileZipEntryRead(t *testing.T) {
	dir := t.TempDir()
	z := buildTestZip(t, map[string]string{
		"small.txt": "hello from inside the zip",
		"big.txt":   strings.Repeat("x", 100),
	})
	writeTestFile(t, dir, "bundle.zip", z)

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{ArchiveEntryMaxSize: 50})

	out, err := tb.readFile(context.Background(), &ReadFileInput{Path: "bundle.zip::small.txt"})
	if err != nil {
		t.Fatalf("readFile zip entry: %v", err)
	}
	if out.Content != "hello from inside the zip" || out.Truncated {
		t.Fatalf("entry content: %q truncated=%v", out.Content, out.Truncated)
	}

	out, err = tb.readFile(context.Background(), &ReadFileInput{Path: "bundle.zip::big.txt"})
	if err != nil {
		t.Fatalf("oversized entry must not error: %v", err)
	}
	if !strings.Contains(out.Content, "too large") {
		t.Fatalf("oversized entry refusal missing: %q", out.Content)
	}

	if _, err := tb.readFile(context.Background(), &ReadFileInput{Path: "bundle.zip::nope.txt"}); err == nil {
		t.Fatal("missing entry must return an error")
	}

	// Entry syntax on a non-archive path is rejected loudly.
	if _, err := tb.readFile(context.Background(), &ReadFileInput{Path: "bundle.zip::"}); err == nil {
		t.Fatal("empty entry name must be rejected")
	}
}

// Entry-list truncation: more than maxArchiveListEntries entries shows the
// cap and the remainder count.
func TestReadFileZipListingTruncation(t *testing.T) {
	dir := t.TempDir()
	members := map[string]string{}
	for i := 0; i < maxArchiveListEntries+50; i++ {
		members[fmt.Sprintf("f%03d.txt", i)] = "x"
	}
	writeTestFile(t, dir, "many.zip", buildTestZip(t, members))

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{})
	out, err := tb.readFile(context.Background(), &ReadFileInput{Path: "many.zip"})
	if err != nil {
		t.Fatalf("readFile zip list: %v", err)
	}
	wantHeader := fmt.Sprintf("zip archive: %d entries, showing first %d", maxArchiveListEntries+50, maxArchiveListEntries)
	if !strings.Contains(out.Content, wantHeader) {
		t.Fatalf("truncation header missing %q:\n%s", wantHeader, out.Content)
	}
	if !strings.Contains(out.Content, "50 more entries not listed") {
		t.Fatalf("remainder trailer missing:\n%s", out.Content)
	}
}

// Oversized archives: listing still works on a random-access backend, but
// entry reads are refused (list-only mandate).
func TestReadFileZipOversizedListOnly(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "huge.zip", buildOversizedZip(t))

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{})
	out, err := tb.readFile(context.Background(), &ReadFileInput{Path: "huge.zip"})
	if err != nil {
		t.Fatalf("oversized zip listing must work: %v", err)
	}
	if !strings.Contains(out.Content, "big.bin") {
		t.Fatalf("oversized listing wrong:\n%s", out.Content)
	}

	out, err = tb.readFile(context.Background(), &ReadFileInput{Path: "huge.zip::big.bin"})
	if err != nil {
		t.Fatalf("entry read on oversized archive must not error: %v", err)
	}
	if !strings.Contains(out.Content, "listing only") {
		t.Fatalf("list-only refusal missing: %q", out.Content)
	}
}

// rar/7z are refused with an explicit unsupported note.
func TestReadFileUnsupportedArchiveFormats(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.rar", []byte("Rar!\x1a\x07\x00"))
	writeTestFile(t, dir, "b.7z", []byte("7z\xbc\xaf\x27\x1c"))

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{})
	for _, name := range []string{"a.rar", "b.7z"} {
		out, err := tb.readFile(context.Background(), &ReadFileInput{Path: name})
		if err != nil {
			t.Fatalf("%s must not error: %v", name, err)
		}
		if !strings.Contains(out.Content, "unsupported archive format") {
			t.Fatalf("%s: unsupported note missing: %q", name, out.Content)
		}
	}
}

// tar and tar.gz list and serve entries via plain streaming — no random
// access needed.
func TestReadFileTarAndTarGz(t *testing.T) {
	dir := t.TempDir()
	entries := map[string]string{
		"pkg/main.go": "package main",
		"docs/intro":  "intro text",
	}
	writeTestFile(t, dir, "src.tar", buildTar(t, false, entries))
	writeTestFile(t, dir, "src.tar.gz", buildTar(t, true, entries))

	tb := newReadFileToolBuilder(t, dir, config.AgentConfig{})
	for _, name := range []string{"src.tar", "src.tar.gz"} {
		out, err := tb.readFile(context.Background(), &ReadFileInput{Path: name})
		if err != nil {
			t.Fatalf("%s list: %v", name, err)
		}
		if !strings.Contains(out.Content, "2 entries") || !strings.Contains(out.Content, "pkg/main.go") {
			t.Fatalf("%s listing wrong:\n%s", name, out.Content)
		}
		out, err = tb.readFile(context.Background(), &ReadFileInput{Path: name + "::docs/intro"})
		if err != nil {
			t.Fatalf("%s entry: %v", name, err)
		}
		if out.Content != "intro text" {
			t.Fatalf("%s entry content: %q", name, out.Content)
		}
	}
}

// The backend capability matrix: native ReaderAt is used directly, a
// seek-only file goes through the adapter, a stream-only backend downloads to
// a temp file within the cap, and beyond the cap gets an explicit degradation
// notice (never a silent retry).
func TestResolveArchiveReaderAtMatrix(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")

	t.Run("native ReaderAt used directly, size recovered via SeekEnd", func(t *testing.T) {
		rc := struct {
			*bytes.Reader
			io.Closer
		}{bytes.NewReader(payload), io.NopCloser(nil)}
		ra, size, notice, cleanup, err := resolveArchiveReaderAt(rc, -1, 1024, "local")
		if err != nil || notice != "" {
			t.Fatalf("direct: err=%v notice=%q", err, notice)
		}
		defer cleanup()
		if size != int64(len(payload)) {
			t.Fatalf("size: got %d, want %d", size, len(payload))
		}
		buf := make([]byte, 4)
		if _, err := ra.ReadAt(buf, 10); err != nil || string(buf) != "abcd" {
			t.Fatalf("ReadAt: %q, %v", buf, err)
		}
	})

	t.Run("seek-only file goes through the adapter", func(t *testing.T) {
		// One shared position, like *nfs.File: Seek moves subsequent Reads.
		br := bytes.NewReader(payload)
		rc := struct {
			io.Reader
			io.Seeker
			io.Closer
		}{br, br, io.NopCloser(nil)}
		ra, size, notice, cleanup, err := resolveArchiveReaderAt(rc, int64(len(payload)), 1024, "nfs")
		if err != nil || notice != "" {
			t.Fatalf("adapter: err=%v notice=%q", err, notice)
		}
		defer cleanup()
		if size != int64(len(payload)) {
			t.Fatalf("size: got %d", size)
		}
		buf := make([]byte, 4)
		if _, err := ra.ReadAt(buf, 10); err != nil || string(buf) != "abcd" {
			t.Fatalf("adapter ReadAt: %q, %v", buf, err)
		}
		if _, err := ra.ReadAt(buf, 0); err != nil || string(buf) != "0123" {
			t.Fatalf("adapter ReadAt rewind: %q, %v", buf, err)
		}
	})

	t.Run("stream-only within cap downloads to a cleaned-up temp file", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("TMPDIR", tmpDir)
		rc := struct {
			io.Reader
			io.Closer
		}{bytes.NewReader(payload), io.NopCloser(nil)}
		ra, size, notice, cleanup, err := resolveArchiveReaderAt(rc, int64(len(payload)), 1024, "ftp")
		if err != nil || notice != "" {
			t.Fatalf("temp: err=%v notice=%q", err, notice)
		}
		if size != int64(len(payload)) {
			t.Fatalf("size: got %d", size)
		}
		buf := make([]byte, 4)
		if _, err := ra.ReadAt(buf, 10); err != nil || string(buf) != "abcd" {
			t.Fatalf("temp ReadAt: %q, %v", buf, err)
		}
		cleanup()
		entries, _ := os.ReadDir(tmpDir)
		if len(entries) != 0 {
			t.Fatalf("temp file not cleaned up: %d entries left in TMPDIR", len(entries))
		}
	})

	t.Run("stream-only beyond cap gets explicit degradation notice", func(t *testing.T) {
		rc := struct {
			io.Reader
			io.Closer
		}{bytes.NewReader(payload), io.NopCloser(nil)}
		_, _, notice, cleanup, err := resolveArchiveReaderAt(rc, 40*1024*1024, 1024, "ftp")
		if err != nil {
			t.Fatalf("degrade: %v", err)
		}
		defer cleanup()
		for _, want := range []string{"does not support random access", `"ftp"`, "Do not retry"} {
			if !strings.Contains(notice, want) {
				t.Fatalf("notice missing %q: %s", want, notice)
			}
		}
	})
}

// End-to-end backend behavior through readFile: NFS-style seek-only backend
// serves zip listings via the adapter; FTP-style stream-only backend serves
// small archives via temp download and refuses oversized ones explicitly.
func TestReadFileZipBackendPaths(t *testing.T) {
	dir := t.TempDir()
	z := buildTestZip(t, map[string]string{"a.txt": "alpha"})
	writeTestFile(t, dir, "ok.zip", z)
	writeTestFile(t, dir, "huge.zip", buildOversizedZip(t))

	base, err := remotefs.NewLocalFS(dir)
	if err != nil {
		t.Fatalf("NewLocalFS: %v", err)
	}

	// NFS-style: seek but no ReaderAt → adapter, listing works.
	tbNFS := NewToolBuilder(nil, seekOnlyFS{base}, 1, 1, NewLogger(nil, 1, 0), config.AgentConfig{}, &db.ScanSession{AllowReadFile: true, Protocol: "nfs"}, false)
	out, err := tbNFS.readFile(context.Background(), &ReadFileInput{Path: "ok.zip"})
	if err != nil {
		t.Fatalf("seek-only listing: %v", err)
	}
	if !strings.Contains(out.Content, "a.txt") {
		t.Fatalf("seek-only listing wrong:\n%s", out.Content)
	}

	// FTP-style: stream only → small archive still works via temp download.
	tbFTP := NewToolBuilder(nil, streamOnlyFS{base}, 1, 1, NewLogger(nil, 1, 0), config.AgentConfig{}, &db.ScanSession{AllowReadFile: true, Protocol: "ftp"}, false)
	out, err = tbFTP.readFile(context.Background(), &ReadFileInput{Path: "ok.zip"})
	if err != nil {
		t.Fatalf("stream-only small listing: %v", err)
	}
	if !strings.Contains(out.Content, "a.txt") {
		t.Fatalf("stream-only listing wrong:\n%s", out.Content)
	}

	// FTP-style oversized → explicit degradation, not a retry loop.
	out, err = tbFTP.readFile(context.Background(), &ReadFileInput{Path: "huge.zip"})
	if err != nil {
		t.Fatalf("stream-only oversized must not error: %v", err)
	}
	for _, want := range []string{"does not support random access", `"ftp"`, "Do not retry"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("degradation notice missing %q: %s", want, out.Content)
		}
	}
}

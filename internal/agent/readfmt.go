package agent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ledongthuc/pdf"
)

const (
	// Zip central directories and PDF xref tables live at the end of the file,
	// so a truncated read cannot be parsed at all.
	maxDocumentReadBytes = 32 << 20
	maxPDFPages          = 50
)

func documentExt(p string) string {
	switch ext := strings.ToLower(path.Ext(p)); ext {
	case ".pdf", ".docx", ".xlsx", ".pptx", ".doc":
		return ext
	}
	return ""
}

// data must be capped at maxDocumentReadBytes+1 by the caller so the
// over-limit case is detectable.
func extractDocument(name string, data []byte, maxChars int) (*ReadFileOutput, error) {
	ext := documentExt(name)
	if ext == ".doc" {
		return &ReadFileOutput{Content: "[unsupported format: .doc (legacy binary Word document) — convert to .docx or .pdf to read its text]"}, nil
	}
	if ext == "" {
		return nil, fmt.Errorf("not a document path: %q", name) // unreachable; callers gate on documentExt
	}
	if len(data) > maxDocumentReadBytes {
		return &ReadFileOutput{Content: fmt.Sprintf("[document too large for text extraction: over the %d MB limit]", maxDocumentReadBytes/(1<<20))}, nil
	}

	var (
		text      string
		truncated bool
		err       error
	)
	switch ext {
	case ".pdf":
		text, truncated, err = extractPDFText(data, maxChars)
	case ".docx":
		text, truncated, err = extractDocxText(data, maxChars)
	case ".xlsx":
		text, truncated, err = extractXlsxText(data, maxChars)
	case ".pptx":
		text, truncated, err = extractPptxText(data, maxChars)
	}
	if err != nil {
		return nil, fmt.Errorf("extract %s text: %w", ext, err)
	}
	if strings.TrimSpace(text) == "" {
		return &ReadFileOutput{Content: "[no extractable text — the document may be image-based or empty]", Truncated: truncated}, nil
	}
	return &ReadFileOutput{Content: text, Truncated: truncated}, nil
}

type runeBudget struct {
	sb        *strings.Builder
	max       int
	written   int
	exhausted bool
}

func (b *runeBudget) writeString(s string) {
	if b.exhausted {
		return
	}
	for _, r := range s {
		if b.written >= b.max {
			b.exhausted = true
			return
		}
		b.sb.WriteRune(r)
		b.written++
	}
}

// One unreadable page is noted inline and skipped rather than sinking the
// whole read.
func extractPDFText(data []byte, maxChars int) (string, bool, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", false, err
	}
	pages := r.NumPage()
	truncated := false
	if pages > maxPDFPages {
		pages = maxPDFPages
		truncated = true
	}
	b := &runeBudget{sb: &strings.Builder{}, max: maxChars}
	for i := 1; i <= pages; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			b.writeString(fmt.Sprintf("[page %d: text extraction failed]\n", i))
			continue
		}
		b.writeString(text)
		if len(text) > 0 && !strings.HasSuffix(text, "\n") {
			b.writeString("\n")
		}
		if b.exhausted {
			truncated = true
			break
		}
	}
	return b.sb.String(), truncated, nil
}

func openMember(zr *zip.Reader, name string) (io.ReadCloser, error) {
	for _, f := range zr.File {
		if f.Name == name {
			return f.Open()
		}
	}
	return nil, fmt.Errorf("%s not found in document", name)
}

// Ordered sheet1, sheet2, ..., sheet10 (length, then lexical).
func memberNames(zr *zip.Reader, dir, suffix string) []string {
	var names []string
	for _, f := range zr.File {
		rest, ok := strings.CutPrefix(f.Name, dir)
		if !ok || !strings.HasSuffix(rest, suffix) || strings.Contains(rest, "/") {
			continue
		}
		names = append(names, f.Name)
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) < len(names[j])
		}
		return names[i] < names[j]
	})
	return names
}

// OOXML text lives in <w:t>/<a:t>-style leaf elements, so matching local
// names regardless of namespace is sufficient.
func xmlTextRuns(xr io.Reader, textTags, breakTags map[string]bool, b *runeBudget) error {
	dec := xml.NewDecoder(xr)
	depth := 0
	textDepth := -1 // depth of the currently open text element, -1 = none
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if textTags[t.Name.Local] {
				textDepth = depth
			}
		case xml.EndElement:
			if depth == textDepth {
				textDepth = -1
			}
			if breakTags[t.Name.Local] {
				b.writeString("\n")
			}
			depth--
		case xml.CharData:
			if textDepth >= 0 {
				b.writeString(string(t))
			}
		}
	}
}

func extractDocxText(data []byte, maxChars int) (string, bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", false, err
	}
	rc, err := openMember(zr, "word/document.xml")
	if err != nil {
		return "", false, err
	}
	defer rc.Close()
	b := &runeBudget{sb: &strings.Builder{}, max: maxChars}
	if err := xmlTextRuns(rc, map[string]bool{"t": true}, map[string]bool{"p": true}, b); err != nil {
		return "", false, err
	}
	return b.sb.String(), b.exhausted, nil
}

func extractPptxText(data []byte, maxChars int) (string, bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", false, err
	}
	names := memberNames(zr, "ppt/slides/", ".xml")
	if len(names) == 0 {
		return "", false, fmt.Errorf("no slides found")
	}
	b := &runeBudget{sb: &strings.Builder{}, max: maxChars}
	for _, name := range names {
		rc, err := openMember(zr, name)
		if err != nil {
			return b.sb.String(), b.exhausted, err
		}
		err = xmlTextRuns(rc, map[string]bool{"t": true}, map[string]bool{"p": true}, b)
		rc.Close()
		if err != nil {
			return b.sb.String(), b.exhausted, err
		}
		b.writeString("\n")
		if b.exhausted {
			break
		}
	}
	return b.sb.String(), b.exhausted, nil
}

func xlsxSharedStrings(zr *zip.Reader) ([]string, error) {
	rc, err := openMember(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil, nil // shared strings are optional
	}
	defer rc.Close()
	var (
		entries []string
		cur     strings.Builder
		inSI    bool
		inT     bool
	)
	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				inT = inSI
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				inSI = false
				entries = append(entries, cur.String())
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT {
				cur.Write(t)
			}
		}
	}
}

func extractXlsxText(data []byte, maxChars int) (string, bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", false, err
	}
	shared, err := xlsxSharedStrings(zr)
	if err != nil {
		return "", false, err
	}
	names := memberNames(zr, "xl/worksheets/", ".xml")
	if len(names) == 0 {
		return "", false, fmt.Errorf("no worksheets found")
	}
	b := &runeBudget{sb: &strings.Builder{}, max: maxChars}
	for _, name := range names {
		rc, err := openMember(zr, name)
		if err != nil {
			return b.sb.String(), b.exhausted, err
		}
		err = renderXlsxSheet(rc, shared, b)
		rc.Close()
		if err != nil {
			return b.sb.String(), b.exhausted, err
		}
		if b.exhausted {
			break
		}
	}
	return b.sb.String(), b.exhausted, nil
}

func renderXlsxSheet(xr io.Reader, shared []string, b *runeBudget) error {
	var (
		cells    []string
		cellType string // t attr of the open <c>: "s" shared, "inlineStr", "" raw
		inCell   bool
		inV      bool
		inT      bool // inside <is><t> of an inline string
		v        strings.Builder
		tText    strings.Builder
	)
	flushCell := func() {
		switch cellType {
		case "s":
			idx, err := strconv.Atoi(strings.TrimSpace(v.String()))
			if err == nil && idx >= 0 && idx < len(shared) {
				cells = append(cells, shared[idx])
			} else {
				cells = append(cells, "")
			}
		case "inlineStr":
			cells = append(cells, tText.String())
		default:
			cells = append(cells, v.String())
		}
		v.Reset()
		tText.Reset()
	}
	dec := xml.NewDecoder(xr)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "c":
				inCell = true
				cellType = ""
				for _, a := range t.Attr {
					if a.Name.Local == "t" {
						cellType = a.Value
					}
				}
			case "v":
				inV = inCell
			case "t":
				inT = inCell
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "c":
				inCell = false
				flushCell()
			case "v":
				inV = false
			case "t":
				inT = false
			case "row":
				b.writeString(strings.Join(cells, "\t"))
				b.writeString("\n")
				cells = nil
			}
		case xml.CharData:
			if inV {
				v.Write(t)
			} else if inT {
				tText.Write(t)
			}
		}
	}
}

// zip needs random access (the central directory is at the end). Local, SFTP
// and SMB files are io.ReaderAt; NFS files only seek, so they get an adapter;
// FTP is stream-only and falls back to a bounded temp-file download.

const (
	maxArchiveListEntries      = 200
	defaultArchiveEntryMaxSize = 64 * 1024
)

func archiveKind(p string) string {
	lp := strings.ToLower(p)
	switch {
	case strings.HasSuffix(lp, ".zip"):
		return "zip"
	case strings.HasSuffix(lp, ".tar"):
		return "tar"
	case strings.HasSuffix(lp, ".tar.gz"), strings.HasSuffix(lp, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(lp, ".rar"):
		return "rar"
	case strings.HasSuffix(lp, ".7z"):
		return "7z"
	}
	return ""
}

// Calls are serialized; archive readers are single-goroutine, so the shared
// seek position is safe.
type seekReaderAt struct {
	mu sync.Mutex
	r  io.Reader
	s  io.Seeker
}

func (a *seekReaderAt) ReadAt(b []byte, off int64) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.s.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	return io.ReadFull(a.r, b)
}

// size may be -1 when Stat failed. A non-empty notice is an explicit
// degradation message the agent must see instead of retrying. cleanup must
// always be called.
func resolveArchiveReaderAt(rc io.ReadCloser, size int64, maxDownload int64, protocol string) (ra io.ReaderAt, actualSize int64, notice string, cleanup func(), err error) {
	cleanup = func() { rc.Close() }

	if ra, ok := rc.(io.ReaderAt); ok {
		if size < 0 {
			if sk, ok2 := rc.(io.Seeker); ok2 {
				if end, serr := sk.Seek(0, io.SeekEnd); serr == nil {
					size = end
					_, _ = sk.Seek(0, io.SeekStart)
				}
			}
		}
		if size >= 0 {
			return ra, size, "", cleanup, nil
		}
	}
	if sk, ok := rc.(io.Seeker); ok && size >= 0 {
		return &seekReaderAt{r: rc, s: sk}, size, "", cleanup, nil
	}

	// Gate on the known size first so oversized archives never start
	// downloading.
	if size > maxDownload {
		return nil, size, archiveDegradeNotice(protocol, size, maxDownload), cleanup, nil
	}
	tmp, err := os.CreateTemp("", "fileengine-zip-*")
	if err != nil {
		cleanup()
		return nil, 0, "", nil, fmt.Errorf("create temp file: %w", err)
	}
	n, err := io.Copy(tmp, io.LimitReader(rc, maxDownload+1))
	rc.Close()
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, 0, "", nil, fmt.Errorf("download archive: %w", err)
	}
	if n > maxDownload {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, n, archiveDegradeNotice(protocol, n, maxDownload), func() {}, nil
	}
	return tmp, n, "", func() { tmp.Close(); os.Remove(tmp.Name()) }, nil
}

// The notice tells the agent not to retry.
func archiveDegradeNotice(protocol string, size, maxDownload int64) string {
	return fmt.Sprintf("[cannot read archive: filesystem backend %q does not support random access and the archive (%s) exceeds the %d MB temporary-download limit — listing and entry reads are both unavailable. Do not retry on this backend; copy the archive to an SMB/SFTP/local filesystem or extract it first]",
		protocol, humanBytes(size), maxDownload/(1<<20))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func listZipArchive(ra io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	total := len(zr.File)
	shown := total
	if shown > maxArchiveListEntries {
		shown = maxArchiveListEntries
	}
	fmt.Fprintf(&sb, "[zip archive: %d entries", total)
	if shown < total {
		fmt.Fprintf(&sb, ", showing first %d", shown)
	}
	sb.WriteString("]\n")
	for _, f := range zr.File[:shown] {
		fmt.Fprintf(&sb, "%12d  %s\n", f.UncompressedSize64, f.Name)
	}
	if shown < total {
		fmt.Fprintf(&sb, "... (%d more entries not listed)\n", total-shown)
	}
	sb.WriteString("[read a text entry: read_file path=\"<archive>::<entry>\" — entries up to the configured size limit]\n")
	return sb.String(), nil
}

// Oversized or binary-looking entries are refused with a note, never an
// error.
func readZipEntry(ra io.ReaderAt, size int64, entryName string, maxEntry int64) (*ReadFileOutput, error) {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name != entryName {
			continue
		}
		if f.UncompressedSize64 > uint64(maxEntry) {
			return &ReadFileOutput{Content: fmt.Sprintf("[archive entry too large to read inline: %s exceeds the %s limit — extract it first]", humanBytes(int64(f.UncompressedSize64)), humanBytes(maxEntry))}, nil
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open entry: %w", err)
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxEntry+1))
		if err != nil {
			return nil, fmt.Errorf("read entry: %w", err)
		}
		return entryTextOutput(data, maxEntry), nil
	}
	return nil, fmt.Errorf("entry %q not found in archive", entryName)
}

func entryTextOutput(data []byte, maxEntry int64) *ReadFileOutput {
	peek := data
	if len(peek) > 1024 {
		peek = peek[:1024]
	}
	if len(peek) > 0 && !isTextContent(string(peek)) {
		return &ReadFileOutput{Content: "[binary archive entry - content not displayable]"}
	}
	truncated := int64(len(data)) > maxEntry
	if truncated {
		data = data[:maxEntry]
	}
	return &ReadFileOutput{Content: string(data), Truncated: truncated}
}

func listTarArchive(r io.Reader, gz bool) (string, error) {
	tr, done, err := tarStream(r, gz)
	if err != nil {
		return "", err
	}
	var (
		sb      strings.Builder
		total   int
		listed  int
		skipped bool
	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			// The read cap cut the stream mid-entry — keep the partial
			// listing; done() below reports the truncation.
			break
		}
		if err != nil {
			return sb.String(), err
		}
		total++
		if listed < maxArchiveListEntries {
			fmt.Fprintf(&sb, "%12d  %s\n", hdr.Size, hdr.Name)
			listed++
		} else {
			skipped = true
		}
	}
	header := fmt.Sprintf("[tar%s archive: %d entries", tarSuffix(gz), total)
	if skipped {
		header += fmt.Sprintf(", showing first %d", maxArchiveListEntries)
	}
	header += "]\n"
	out := header + sb.String()
	if skipped {
		out += fmt.Sprintf("... (%d more entries not listed)\n", total-maxArchiveListEntries)
	}
	if !done() {
		out += fmt.Sprintf("[listing truncated: archive stream exceeds the %d MB read cap]\n", maxDocumentReadBytes/(1<<20))
	}
	out += "[read a text entry: read_file path=\"<archive>::<entry>\" — entries up to the configured size limit]\n"
	return out, nil
}

// The entry size is known from its header before any content is read, so
// oversized entries are refused without downloading.
func readTarEntry(r io.Reader, gz bool, entryName string, maxEntry int64) (*ReadFileOutput, error) {
	tr, _, err := tarStream(r, gz)
	if err != nil {
		return nil, err
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("entry %q not found in archive", entryName)
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name != entryName {
			continue
		}
		if hdr.Size > maxEntry {
			return &ReadFileOutput{Content: fmt.Sprintf("[archive entry too large to read inline: %s exceeds the %s limit — extract it first]", humanBytes(hdr.Size), humanBytes(maxEntry))}, nil
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxEntry+1))
		if err != nil {
			return nil, fmt.Errorf("read entry: %w", err)
		}
		return entryTextOutput(data, maxEntry), nil
	}
}

func tarStream(r io.Reader, gz bool) (*tar.Reader, func() bool, error) {
	cr := &countingReader{r: io.LimitReader(r, maxDocumentReadBytes+1)}
	var tr *tar.Reader
	if gz {
		gzr, err := gzip.NewReader(cr)
		if err != nil {
			return nil, nil, fmt.Errorf("open gzip stream: %w", err)
		}
		tr = tar.NewReader(gzr)
	} else {
		tr = tar.NewReader(cr)
	}
	done := func() bool { return cr.n <= maxDocumentReadBytes }
	return tr, done, nil
}

func tarSuffix(gz bool) string {
	if gz {
		return ".gz"
	}
	return ""
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

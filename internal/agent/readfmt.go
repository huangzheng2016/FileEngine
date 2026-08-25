package agent

// Format-aware text extraction for the read_file tool: PDF via
// github.com/ledongthuc/pdf (a maintained pure-Go fork of rsc.io/pdf), and
// docx/xlsx/pptx via archive/zip + encoding/xml from the standard library —
// OOXML files are zip containers of XML members, so text extraction needs no
// third-party dependency. The legacy binary .doc format is explicitly
// unsupported (reported as such, never parsed).

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
)

const (
	// maxDocumentReadBytes caps how many bytes of a document file are read
	// for text extraction. Zip/central-directory and PDF xref tables live at
	// the end of the file, so a truncated read cannot be parsed at all —
	// beyond the cap the tool reports "too large" instead of a parse error.
	maxDocumentReadBytes = 32 << 20 // 32 MB
	// maxPDFPages caps how many pages of one PDF are extracted.
	maxPDFPages = 50
)

// documentExt returns the lower-cased extension when p names a document
// format read_file can extract text from ("" otherwise).
func documentExt(p string) string {
	switch ext := strings.ToLower(path.Ext(p)); ext {
	case ".pdf", ".docx", ".xlsx", ".pptx", ".doc":
		return ext
	}
	return ""
}

// extractDocument turns a document file's raw bytes into plain text. maxChars
// caps the extracted text (Truncated reports the cut). data must be capped at
// maxDocumentReadBytes+1 by the caller so the over-limit case is detectable.
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

// runeBudget caps how many runes get written to sb across a whole document;
// exhausted reports that the cap was hit (i.e. the output is truncated).
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

// ============ PDF ============

// extractPDFText extracts per-page plain text, capped at maxPDFPages pages
// and maxChars runes. One unreadable page is noted inline and skipped rather
// than sinking the whole read.
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

// ============ OOXML (docx / xlsx / pptx) ============

// openMember opens one named member of a zip document.
func openMember(zr *zip.Reader, name string) (io.ReadCloser, error) {
	for _, f := range zr.File {
		if f.Name == name {
			return f.Open()
		}
	}
	return nil, fmt.Errorf("%s not found in document", name)
}

// memberNames lists zip members directly under dir with the given suffix,
// ordered sheet1..sheet2..sheet10 style (length-then-lexical sort).
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

// xmlTextRuns streams xr as XML, collecting character data of elements whose
// local name is in textTags into the budget; closing a breakTags element
// appends a newline. OOXML text lives in <w:t>/<a:t>-style leaf elements, so
// matching local names (ignoring namespaces) is sufficient.
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

// extractDocxText pulls paragraph text out of word/document.xml: <w:t> runs
// as text, <w:p> ends as newlines.
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

// extractPptxText pulls text runs (<a:t>, paragraphs <a:p>) out of every
// slide in order.
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

// ============ XLSX ============

// xlsxSharedStrings parses xl/sharedStrings.xml: each <si> entry is the
// concatenation of its <t> runs. Returns nil when the workbook has none.
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

// extractXlsxText renders every worksheet as TSV lines (cells joined by tab,
// rows by newline). Shared-string cells (t="s") are resolved through
// xl/sharedStrings.xml; inline strings and raw values are used as-is.
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

// renderXlsxSheet streams one worksheet, resolving cell values and writing
// TSV rows into the budget.
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

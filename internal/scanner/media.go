package scanner

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"FileEngine/internal/db"

	"github.com/rwcarlsen/goexif/exif"
)

const (
	DateSourceExif     = "exif"
	DateSourceVideo    = "video"
	DateSourceFilename = "filename"
	DateSourceMtime    = "mtime"
)

// headerBytes is the ranged read for media headers: EXIF lives in the first
// APP1 segment / first IFDs, the MP4 box index starts at offset 0.
const headerBytes = 128 << 10

var (
	imageExts = map[string]bool{
		"jpg": true, "jpeg": true, "heic": true, "heif": true, "tif": true, "tiff": true,
		"png": true, "dng": true, "cr2": true, "nef": true, "arw": true,
	}
	videoExts = map[string]bool{"mp4": true, "mov": true, "m4v": true, "3gp": true}
)

func extOf(name string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
}

type mediaInfo struct {
	takenAt *time.Time
	camera  string
	hasGPS  bool
	source  string
}

func (m mediaInfo) cols() map[string]any {
	return map[string]any{"taken_at": m.takenAt, "camera": m.camera, "has_gps": m.hasGPS, "date_source": m.source}
}

// Per-file read or parse failures fall back to the file name or mtime; only
// cancellation and DB errors fail the scan.
func (s *Scanner) extractMetadata(ctx context.Context, sessionID uint) error {
	jobs := make(chan db.MediaCandidate, 64)
	pass, cancel := context.WithCancel(ctx)
	defer cancel()
	w := newWriter(s.repo, cancel)

	var wg sync.WaitGroup
	for range s.readConcurrency() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				if pass.Err() != nil {
					continue
				}
				s.progress.setPath(c.OriginalPath)
				info := s.mediaMeta(pass, c)
				w.send(writeBatch{updates: []db.EntryUpdate{{ID: c.ID, Cols: info.cols()}}})
			}
		}()
	}

	var cursor uint
	var readErr error
	for pass.Err() == nil {
		rows, err := s.repo.MediaCandidatesAfter(sessionID, cursor, 5000)
		if err != nil {
			readErr = err
			break
		}
		for _, r := range rows {
			if ext := extOf(r.Name); imageExts[ext] || videoExts[ext] {
				jobs <- r
			}
		}
		if len(rows) < 5000 {
			break
		}
		cursor = rows[len(rows)-1].ID
	}
	close(jobs)
	wg.Wait()
	if err := w.close(); err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	return ctx.Err()
}

func (s *Scanner) mediaMeta(ctx context.Context, c db.MediaCandidate) mediaInfo {
	ext := extOf(c.Name)
	var info mediaInfo
	if rc, err := s.fs.ReadFile(ctx, c.OriginalPath); err != nil {
		log.Printf("warn: metadata: open %s: %v", c.OriginalPath, err)
	} else {
		buf := make([]byte, headerBytes)
		n, _ := io.ReadFull(rc, buf)
		buf = buf[:n]
		if imageExts[ext] {
			info = parseImageMeta(buf)
		} else {
			info = parseVideoMeta(buf, c.Size, randomAccess(rc))
		}
		rc.Close()
	}
	if info.takenAt == nil {
		if t, ok := filenameDate(c.Name); ok {
			info.takenAt, info.source = &t, DateSourceFilename
		} else {
			mt := c.ModTime
			info.takenAt, info.source = &mt, DateSourceMtime
		}
	}
	return info
}

func randomAccess(rc io.ReadCloser) io.ReaderAt {
	if ra, ok := rc.(io.ReaderAt); ok {
		return ra
	}
	if sk, ok := rc.(io.ReadSeeker); ok {
		return &seekReaderAt{rs: sk}
	}
	return nil
}

type seekReaderAt struct{ rs io.ReadSeeker }

func (a *seekReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if _, err := a.rs.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	return io.ReadFull(a.rs, p)
}

// exifTimeLayout is EXIF's naive local wall-clock format; it is stored as UTC
// so year/month templates see the wall-clock date.
const exifTimeLayout = "2006:01:02 15:04:05"

// JPEG APP1, TIFF-based raws (DNG/CR2/NEF/ARW), and the embedded EXIF blob of
// HEIC / PNG eXIf.
func parseImageMeta(buf []byte) (info mediaInfo) {
	defer func() {
		if recover() != nil {
			info = mediaInfo{}
		}
	}()
	data := buf
	isJPEG := len(buf) > 2 && buf[0] == 0xFF && buf[1] == 0xD8
	isTIFF := bytes.HasPrefix(buf, []byte("II*\x00")) || bytes.HasPrefix(buf, []byte("MM\x00*"))
	if !isJPEG && !isTIFF {
		if i := bytes.Index(buf, []byte("Exif\x00\x00")); i >= 0 {
			data = buf[i:] // HEIC Exif item
		} else if i := bytes.Index(buf, []byte("eXIf")); i >= 0 {
			data = buf[i+4:] // PNG chunk payload is a bare TIFF stream
		} else {
			return mediaInfo{}
		}
	}
	x, err := exif.Decode(bytes.NewReader(data))
	if err != nil || x == nil {
		return mediaInfo{}
	}
	for _, field := range []exif.FieldName{exif.DateTimeOriginal, exif.DateTimeDigitized, exif.DateTime} {
		tag, err := x.Get(field)
		if err != nil {
			continue
		}
		v, err := tag.StringVal()
		if err != nil {
			continue
		}
		t, err := time.Parse(exifTimeLayout, strings.TrimRight(strings.TrimSpace(v), "\x00"))
		if err != nil || t.Year() < 1900 {
			continue
		}
		info.takenAt, info.source = &t, DateSourceExif
		break
	}
	if tag, err := x.Get(exif.Model); err == nil {
		if v, err := tag.StringVal(); err == nil {
			info.camera = strings.TrimSpace(strings.TrimRight(v, "\x00"))
		}
	}
	if _, _, err := x.LatLong(); err == nil {
		info.hasGPS = true
	}
	return info
}

var mp4Epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

// When moov sits after the media data the top-level box chain is followed
// through ra; without a ReaderAt it is skipped.
func parseVideoMeta(head []byte, size int64, ra io.ReaderAt) mediaInfo {
	read := func(off int64, n int) []byte {
		if off < 0 || n <= 0 {
			return nil
		}
		if off+int64(n) <= int64(len(head)) {
			return head[off : off+int64(n)]
		}
		if ra == nil {
			return nil
		}
		b := make([]byte, n)
		got, _ := ra.ReadAt(b, off)
		return b[:got]
	}
	if size <= 0 {
		size = int64(len(head))
	}
	var off int64
	for i := 0; i < 64 && off+8 <= size; i++ {
		hdr := read(off, 16)
		if len(hdr) < 8 {
			return mediaInfo{}
		}
		boxSize := int64(binary.BigEndian.Uint32(hdr[0:4]))
		typ := string(hdr[4:8])
		hdrLen := int64(8)
		switch boxSize {
		case 1:
			if len(hdr) < 16 {
				return mediaInfo{}
			}
			boxSize = int64(binary.BigEndian.Uint64(hdr[8:16]))
			hdrLen = 16
		case 0:
			boxSize = size - off
		}
		if boxSize < hdrLen {
			return mediaInfo{}
		}
		if typ == "moov" {
			// mvhd is (by spec, in practice) the first child of moov.
			body := read(off+hdrLen, int(min(boxSize-hdrLen, 4096)))
			if t, ok := mvhdTime(body); ok {
				return mediaInfo{takenAt: &t, source: DateSourceVideo}
			}
			return mediaInfo{}
		}
		off += boxSize
	}
	return mediaInfo{}
}

func mvhdTime(body []byte) (time.Time, bool) {
	for off := 0; off+8 <= len(body); {
		sz := int(binary.BigEndian.Uint32(body[off : off+4]))
		if string(body[off+4:off+8]) == "mvhd" {
			p := body[off+8:]
			if len(p) < 4 {
				return time.Time{}, false
			}
			var secs uint64
			if p[0] == 1 {
				if len(p) < 12 {
					return time.Time{}, false
				}
				secs = binary.BigEndian.Uint64(p[4:12])
			} else {
				if len(p) < 8 {
					return time.Time{}, false
				}
				secs = uint64(binary.BigEndian.Uint32(p[4:8]))
			}
			if secs == 0 {
				return time.Time{}, false // unset by the muxer
			}
			t := mp4Epoch.Add(time.Duration(secs) * time.Second)
			return t, t.Year() >= 1970
		}
		if sz < 8 {
			return time.Time{}, false
		}
		off += sz
	}
	return time.Time{}, false
}

var filenameDatePattern = regexp.MustCompile(
	`(?:^|\D)((?:19|20)\d\d)[-_.]?(0[1-9]|1[0-2])[-_.]?(0[1-9]|[12]\d|3[01])` +
		`(?:[-_ T.]?([01]\d|2[0-3])[-_.:]?([0-5]\d)[-_.:]?([0-5]\d))?(?:\D|$)`)

func filenameDate(name string) (time.Time, bool) {
	for _, m := range filenameDatePattern.FindAllStringSubmatch(name, -1) {
		n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
		y, mo, d := n(1), n(2), n(3)
		t := time.Date(y, time.Month(mo), d, n(4), n(5), n(6), 0, time.UTC)
		if t.Day() != d || t.Month() != time.Month(mo) {
			continue // e.g. Feb 30
		}
		return t, true
	}
	return time.Time{}, false
}

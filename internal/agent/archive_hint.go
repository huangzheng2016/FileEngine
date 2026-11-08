package agent

// zip reads only the central directory (a few tail seeks); tar streams
// headers bounded by the 32MB read cap. Any failure just drops the hint.

import (
	"archive/tar"
	"archive/zip"
	"context"
	"fmt"
	"io"
	"strings"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

const archiveHintNoisePrefix = "__MACOSX/"

func zipHint(ra io.ReaderAt, size int64, maxNames int) (string, error) {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return "", err
	}
	total := 0
	names := make([]string, 0, maxNames)
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, archiveHintNoisePrefix) {
			continue
		}
		total++
		if len(names) < maxNames && !strings.HasSuffix(f.Name, "/") {
			names = append(names, f.Name)
		}
	}
	return formatArchiveHint("zip", total, names, false), nil
}

func tarHint(r io.Reader, gz bool, maxNames int) (string, error) {
	tr, _, err := tarStream(r, gz)
	if err != nil {
		return "", err
	}
	total := 0
	capped := false
	names := make([]string, 0, maxNames)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Truncated/corrupt stream: keep the partial signal.
			capped = true
			break
		}
		if strings.HasPrefix(hdr.Name, archiveHintNoisePrefix) {
			continue
		}
		total++
		if len(names) < maxNames && hdr.Typeflag != tar.TypeDir {
			names = append(names, hdr.Name)
		}
	}
	return formatArchiveHint("tar"+tarSuffix(gz), total, names, capped), nil
}

func formatArchiveHint(kind string, total int, names []string, capped bool) string {
	more := ""
	if capped {
		more = "+"
	}
	if len(names) == 0 {
		return fmt.Sprintf("%s 压缩包，共 %d%s 个条目", kind, total, more)
	}
	return fmt.Sprintf("%s 压缩包，共 %d%s 个条目，含: %s", kind, total, more, strings.Join(names, ", "))
}

func computeArchiveHint(ctx context.Context, fsys remotefs.RemoteFS, protocol, path string, size int64, kind string, maxNames int) (string, error) {
	rc, err := fsys.ReadFile(ctx, path)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	if kind == "zip" {
		ra, sz, _, cleanup, err := resolveArchiveReaderAt(rc, size, maxDocumentReadBytes, protocol)
		if err != nil {
			return "", err
		}
		defer cleanup()
		return zipHint(ra, sz, maxNames)
	}
	return tarHint(rc, kind == "tar.gz", maxNames)
}

// harnessArchiveHintsPerPull bounds archive opens per untagged harness pull —
// each hint is a remote seek (zip) or a bounded header stream (tar), and over
// a high-latency link this caps the per-pull cost while still covering
// realistic batches.
const harnessArchiveHintsPerPull = 20

func fsProtocolLabel(fsys remotefs.RemoteFS) string {
	switch fsys.(type) {
	case *remotefs.NFSFS:
		return "nfs"
	case *remotefs.FTPFS:
		return "ftp"
	case *remotefs.SMBFS:
		return "smb"
	case *remotefs.SFTPFS:
		return "sftp"
	case *remotefs.LocalFS:
		return "local"
	}
	return ""
}

func (a *AutoTagAgent) archiveHints(ctx context.Context, fsys remotefs.RemoteFS, files []db.FileEntry, cfg config.AgentConfig) map[uint]string {
	if cfg.ArchivePromptDisabled {
		return nil
	}
	maxNames := cfg.ArchivePromptMaxEntries
	if maxNames <= 0 {
		maxNames = 6
	}
	var hints map[uint]string
	for _, f := range files {
		kind := archiveKind(f.Name)
		if kind != "zip" && kind != "tar" && kind != "tar.gz" {
			continue
		}
		hint, err := computeArchiveHint(ctx, fsys, a.fsCfg.Protocol, f.OriginalPath, f.Size, kind, maxNames)
		if err != nil || hint == "" {
			continue
		}
		if hints == nil {
			hints = make(map[uint]string)
		}
		hints[f.ID] = hint
	}
	return hints
}

package agent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
	"FileEngine/internal/pathutil"
	"FileEngine/internal/remotefs"
)

type ToolBuilder struct {
	repo               *db.Repository
	fs                 remotefs.RemoteFS
	sessionID          uint
	filesystemID       uint
	logger             *Logger
	cfg                config.AgentConfig
	session            *model.ScanSession
	modelVisionEnabled bool
	guard              categoryGuard

	// Tool-originated events carry RunID; 0 outside a run.
	RunID uint
	// RunKind is a db.RunPhase* value. It gates handlers (survey never
	// decides; tagging rules need a path_prefix); empty means no gate.
	RunKind  string
	unitRoot string
	notes    notesCursor
}

func NewToolBuilder(repo *db.Repository, fs remotefs.RemoteFS, sessionID uint, filesystemID uint, logger *Logger, cfg config.AgentConfig, session *model.ScanSession, modelVisionEnabled bool) *ToolBuilder {
	return &ToolBuilder{
		repo:               repo,
		fs:                 fs,
		sessionID:          sessionID,
		filesystemID:       filesystemID,
		logger:             logger,
		cfg:                cfg,
		session:            session,
		modelVisionEnabled: modelVisionEnabled,
	}
}

func normalizePath(p string) string {
	return strings.TrimPrefix(p, "/")
}

func ptrStr(s string) *string {
	return &s
}

const defaultReadChars = 8000

func (tb *ToolBuilder) readPageChars() int {
	if tb.cfg.MaxFileReadSize > 0 {
		return tb.cfg.MaxFileReadSize
	}
	return defaultReadChars
}

const sensitiveNotice = "敏感文件（密钥/凭证/证件/税务类），不读取内容；只按文件名和位置判断"

// A name is sensitive when any of its path segments is, so archive entries
// such as ".ssh/id_rsa" are covered as well as plain paths.
func hasSensitiveSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg != "" && db.IsSensitiveName(seg) {
			return true
		}
	}
	return false
}

func (tb *ToolBuilder) isSensitivePath(p string) bool {
	archive, entry, hasEntry := strings.Cut(p, "::")
	if db.IsSensitiveName(archive) || (hasEntry && hasSensitiveSegment(entry)) {
		return true
	}
	if tb.repo == nil {
		return false
	}
	f, err := tb.repo.GetFileByPath(tb.sessionID, strings.Trim(normalizePath(archive), "/"))
	return err == nil && f.Sensitive
}

// readFileTool is the read_file handler: switch gate, sensitive check, and
// every model-fixable failure as a success:false result.
func (tb *ToolBuilder) readFileTool(ctx context.Context, input *ReadFileInput) (*ReadFileOutput, error) {
	if !tb.session.AllowReadFile {
		return failed[ReadFileOutput]("本任务未开启读取文件内容", "只按文件名、目录列表和信号判断"), nil
	}
	if tb.isSensitivePath(input.Path) {
		return failed[ReadFileOutput](sensitiveNotice), nil
	}
	out, err := tb.readFile(ctx, input)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return failed[ReadFileOutput]("无法读取："+err.Error(), "确认路径来自目录列表；读压缩包内的文件用 压缩包路径::条目路径"), nil
	}
	res := *out
	res.Success = true
	res.Content = WrapUntrusted(input.Path, out.Content)
	return &res, nil
}

func (tb *ToolBuilder) readFile(ctx context.Context, input *ReadFileInput) (*ReadFileOutput, error) {
	if tb.isSensitivePath(input.Path) {
		return &ReadFileOutput{Content: "[" + sensitiveNotice + "]"}, nil
	}
	limit := tb.readPageChars()
	offset := max(input.Offset, 0)
	out, err := tb.readFileRaw(ctx, input, offset+limit)
	if err != nil || out == nil {
		return out, err
	}
	runes := []rune(out.Content)
	if offset >= len(runes) {
		return &ReadFileOutput{Content: ""}, nil
	}
	page := runes[offset:]
	if len(page) > limit {
		page = page[:limit]
	}
	res := &ReadFileOutput{Content: string(page), Truncated: out.Truncated || offset+len(page) < len(runes)}
	if res.Truncated {
		res.NextOffset = offset + len(page)
	}
	return res, nil
}

func (tb *ToolBuilder) readFileRaw(ctx context.Context, input *ReadFileInput, maxSize int) (*ReadFileOutput, error) {
	// Split first; the filesystem-facing part is validated like every other
	// path.
	archivePath, entryName, hasEntry := strings.Cut(input.Path, "::")
	if err := pathutil.ValidateFSPath(archivePath); err != nil {
		return nil, fmt.Errorf("路径不合法：%w", err)
	}
	if hasEntry && entryName == "" {
		return nil, fmt.Errorf("压缩包条目名为空：%s", SanitizeUntrustedName(input.Path))
	}

	ext := documentExt(archivePath)
	arch := archiveKind(archivePath)
	if hasEntry && arch == "" {
		return nil, fmt.Errorf("「::」只能用于 zip/tar 压缩包：%s", SanitizeUntrustedName(input.Path))
	}

	if arch != "" {
		return tb.readArchive(ctx, input, archivePath, entryName, arch, maxSize)
	}

	// Documents must be parsed whole (zip central directories and PDF xref
	// tables are at the end), so they get a larger read budget.
	readCap := int64(maxSize)*4 + 1
	if ext != "" {
		readCap = maxDocumentReadBytes + 1
	}

	reader, err := tb.fs.ReadFile(ctx, normalizePath(archivePath))
	if err != nil {
		return nil, fmt.Errorf("读取文件失败：%w", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, readCap))
	if err != nil {
		return nil, fmt.Errorf("读取出错：%w", err)
	}

	if ext != "" {
		out, err := extractDocument(archivePath, data, maxSize)
		if err != nil {
			return nil, err
		}
		return out, nil
	}

	peek := data
	if len(peek) > 1024 {
		peek = peek[:1024]
	}
	if len(peek) > 0 && !isTextContent(string(peek)) {
		out := &ReadFileOutput{Content: "[二进制文件，无法显示文本]", Truncated: false}
		return out, nil
	}

	content, cut := capRunes(string(data), maxSize)
	out := &ReadFileOutput{Content: content, Truncated: cut || int64(len(data)) >= readCap}
	return out, nil
}

func (tb *ToolBuilder) readArchive(ctx context.Context, input *ReadFileInput, archivePath, entryName, kind string, maxChars int) (*ReadFileOutput, error) {
	log := func(out *ReadFileOutput) (*ReadFileOutput, error) {
		return out, nil
	}

	if kind == "rar" || kind == "7z" {
		return log(&ReadFileOutput{Content: fmt.Sprintf("[不支持的压缩格式 .%s；支持 zip、tar、tar.gz/tgz]", kind)})
	}

	maxEntry := int64(tb.cfg.ArchiveEntryMaxSize)
	if maxEntry <= 0 {
		maxEntry = defaultArchiveEntryMaxSize
	}

	rc, err := tb.fs.ReadFile(ctx, normalizePath(archivePath))
	if err != nil {
		return nil, fmt.Errorf("读取文件失败：%w", err)
	}

	if kind == "tar" || kind == "tar.gz" {
		defer rc.Close()
		gz := kind == "tar.gz"
		if entryName != "" {
			out, err := readTarEntry(rc, gz, entryName, maxEntry)
			if err != nil {
				return nil, err
			}
			return log(out)
		}
		listing, err := listTarArchive(rc, gz)
		if err != nil {
			return nil, fmt.Errorf("读取 tar 列表失败：%w", err)
		}
		content, cut := capRunes(listing, maxChars)
		return log(&ReadFileOutput{Content: content, Truncated: cut})
	}

	// Entry reads are gated on the archive size; listing is not, since the
	// central directory alone is cheap on random-access backends.
	size := int64(-1)
	if st, serr := tb.fs.Stat(ctx, normalizePath(archivePath)); serr == nil {
		size = st.Size
	}
	if entryName != "" && size > maxDocumentReadBytes {
		rc.Close()
		return log(&ReadFileOutput{Content: fmt.Sprintf("[压缩包过大（%s，超过 %d MB 上限）：不能读取包内条目，只能看列表]",
			humanBytes(size), maxDocumentReadBytes/(1<<20))})
	}
	ra, actualSize, notice, cleanup, err := resolveArchiveReaderAt(rc, size, maxDocumentReadBytes, tb.session.Protocol)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	if notice != "" {
		return log(&ReadFileOutput{Content: notice})
	}
	if entryName != "" {
		out, err := readZipEntry(ra, actualSize, entryName, maxEntry)
		if err != nil {
			return nil, err
		}
		return log(out)
	}
	listing, err := listZipArchive(ra, actualSize)
	if err != nil {
		return nil, fmt.Errorf("读取 zip 列表失败：%w", err)
	}
	content, cut := capRunes(listing, maxChars)
	return log(&ReadFileOutput{Content: content, Truncated: cut})
}

func capRunes(s string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		return s, false
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s, false
	}
	return string(runes[:maxChars]), true
}

// Samples several positions so NUL bytes past the first 512 bytes are not
// missed.
func isTextContent(s string) bool {
	if len(s) == 0 {
		return true
	}
	positions := []int{0, len(s) / 2, len(s) - 512}
	for _, start := range positions {
		if start < 0 {
			start = 0
		}
		end := start + 512
		if end > len(s) {
			end = len(s)
		}
		for i := start; i < end; i++ {
			if s[i] == 0 {
				return false
			}
		}
	}
	return true
}

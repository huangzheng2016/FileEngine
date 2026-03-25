package scanner

import (
	"context"
	"fmt"
	"log"
	"path"
	"strings"

	"FileEngine/internal/db"
	"FileEngine/internal/remotefs"
)

type Scanner struct {
	fs   remotefs.RemoteFS
	repo *db.Repository
}

func New(fs remotefs.RemoteFS, repo *db.Repository) *Scanner {
	return &Scanner{fs: fs, repo: repo}
}

func (s *Scanner) Scan(ctx context.Context, session *db.ScanSession) error {
	session.Status = "scanning"
	if err := s.repo.UpdateSession(session); err != nil {
		return err
	}

	rootPath := session.RootPath
	var allFiles []db.FileEntry

	if err := s.scanDir(ctx, rootPath, rootPath, 0, session.ID, &allFiles); err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	if err := db.WithRetry(func() error { return s.repo.CreateFiles(allFiles) }); err != nil {
		return fmt.Errorf("save files: %w", err)
	}

	// Update child counts for directories
	s.updateChildCounts(session.ID, allFiles)

	session.Status = "scanned"
	session.TotalFiles = len(allFiles)
	return s.repo.UpdateSession(session)
}

func (s *Scanner) scanDir(ctx context.Context, currentPath, rootPath string, depth int, sessionID uint, result *[]db.FileEntry) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	entries, err := s.fs.List(ctx, currentPath)
	if err != nil {
		log.Printf("warn: cannot list %s: %v", currentPath, err)
		return nil
	}

	parentPath := currentPath
	if currentPath == rootPath {
		parentPath = ""
	}

	for _, entry := range entries {
		entryPath := entry.Path
		if !path.IsAbs(entryPath) {
			entryPath = path.Join(currentPath, entry.Name)
		}

		relDepth := depth
		if currentPath != rootPath {
			relDepth = depth
		}

		pp := parentPath
		if pp == rootPath {
			pp = rootPath
		}

		fe := db.FileEntry{
			ScanSessionID: sessionID,
			OriginalPath:  entryPath,
			Name:          entry.Name,
			Size:          entry.Size,
			ModTime:       entry.ModTime,
			Permissions:   entry.Permissions,
			FileType:      string(entry.FileType),
			ParentPath:    pp,
			Depth:         relDepth,
		}
		*result = append(*result, fe)

		if entry.IsDir {
			if err := s.scanDir(ctx, entryPath, rootPath, depth+1, sessionID, result); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Scanner) updateChildCounts(sessionID uint, files []db.FileEntry) {
	childMap := make(map[string]int)
	for _, f := range files {
		childMap[f.ParentPath]++
	}

	for parentPath, count := range childMap {
		if parentPath == "" {
			continue
		}
		f, err := s.repo.GetFileByPath(sessionID, parentPath)
		if err != nil {
			continue
		}
		f.ChildCount = count
		s.repo.UpdateFile(f)
	}

	// Also count root-level children for the root directory entry
	rootCount := 0
	for _, f := range files {
		if strings.Count(strings.TrimPrefix(f.OriginalPath, files[0].ParentPath), "/") <= 1 {
			rootCount++
		}
	}
}

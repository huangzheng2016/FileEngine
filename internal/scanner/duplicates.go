package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"sort"
	"sync"

	"FileEngine/internal/db"
)

// partialChunk is the size of each end of a file the partial hash reads.
const partialChunk = 64 << 10

// detectDuplicates groups content-identical files: size buckets (> 1 file,
// size > 0) → partial hash (first + last 64KB) → full sha256 for partial
// collisions. Every group's original is the oldest file (then shortest path,
// then path, then id); the others get DuplicateOf and, when still undecided,
// role duplicate. Links are recomputed from scratch each scan; full hashes of
// unchanged files are reused. With enabled=false all links and hashes are
// cleared. Unreadable files are skipped, never fatal.
func (s *Scanner) detectDuplicates(ctx context.Context, sessionID uint, enabled bool) error {
	if err := s.repo.ResetDuplicates(sessionID, !enabled); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	sizes, err := s.repo.DuplicateSizeBuckets(sessionID)
	if err != nil {
		return err
	}
	pass, cancel := context.WithCancel(ctx)
	defer cancel()
	w := newWriter(s.repo, cancel)
	groups := 0
	for _, size := range sizes {
		if pass.Err() != nil {
			break
		}
		rows, err := s.repo.FilesOfSize(sessionID, size)
		if err != nil {
			w.close()
			return err
		}
		groups += s.dedupeBucket(pass, rows, w)
	}
	if err := w.close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	log.Printf("scan session %d: %d duplicate groups across %d size buckets", sessionID, groups, len(sizes))
	return nil
}

// dedupeBucket hashes one same-size bucket and queues hash / duplicate
// updates. Returns the number of duplicate groups found.
func (s *Scanner) dedupeBucket(ctx context.Context, rows []db.DupCandidate, w *scanWriter) int {
	size := rows[0].Size
	var need []int // indexes of rows without a stored full hash
	for i, r := range rows {
		if r.Hash == "" {
			need = append(need, i)
		}
	}
	full := func(idx []int) {
		hashes := s.hashAll(ctx, rows, idx, s.fullHash)
		for i, h := range hashes {
			rows[i].Hash = h
			w.send(writeBatch{updates: []db.EntryUpdate{{ID: rows[i].ID, Cols: map[string]any{"hash": h}}}})
		}
	}
	switch {
	case len(need) == 0:
	case len(need) < len(rows) || size <= 2*partialChunk:
		// Stored hashes are full hashes, and small files read whole anyway:
		// a partial pass would save nothing.
		full(need)
	default:
		partial := s.hashAll(ctx, rows, need, s.partialHash)
		byPartial := map[string][]int{}
		for i, h := range partial {
			byPartial[h] = append(byPartial[h], i)
		}
		var collide []int
		for _, idx := range byPartial {
			if len(idx) > 1 {
				collide = append(collide, idx...)
			}
		}
		sort.Ints(collide)
		full(collide)
	}

	byHash := map[string][]db.DupCandidate{}
	for _, r := range rows {
		if r.Hash != "" {
			byHash[r.Hash] = append(byHash[r.Hash], r)
		}
	}
	groups := 0
	for _, g := range byHash {
		if len(g) < 2 {
			continue
		}
		groups++
		sort.Slice(g, func(i, j int) bool { return originalFirst(g[i], g[j]) })
		orig := g[0].ID
		for _, d := range g[1:] {
			cols := map[string]any{"duplicate_of": orig}
			if d.Role == db.RoleUndecided {
				cols["role"] = db.RoleDuplicate
			}
			w.send(writeBatch{updates: []db.EntryUpdate{{ID: d.ID, Cols: cols}}})
		}
	}
	return groups
}

// originalFirst is the deterministic "which copy is the original" order:
// oldest mtime, then shortest path, then path, then id.
func originalFirst(a, b db.DupCandidate) bool {
	if !a.ModTime.Equal(b.ModTime) {
		return a.ModTime.Before(b.ModTime)
	}
	if len(a.OriginalPath) != len(b.OriginalPath) {
		return len(a.OriginalPath) < len(b.OriginalPath)
	}
	if a.OriginalPath != b.OriginalPath {
		return a.OriginalPath < b.OriginalPath
	}
	return a.ID < b.ID
}

// hashAll hashes rows[idx...] with bounded workers; failed files are left out
// of the result.
func (s *Scanner) hashAll(ctx context.Context, rows []db.DupCandidate, idx []int,
	hash func(context.Context, db.DupCandidate) (string, error)) map[int]string {
	out := map[int]string{}
	var mu sync.Mutex
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(s.readConcurrency(), len(idx)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					continue
				}
				s.progress.setPath(rows[i].OriginalPath)
				h, err := hash(ctx, rows[i])
				if err != nil {
					log.Printf("warn: duplicates: hash %s: %v", rows[i].OriginalPath, err)
					continue
				}
				mu.Lock()
				out[i] = h
				mu.Unlock()
			}
		}()
	}
	for _, i := range idx {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return out
}

// partialHash hashes the first and last 64KB. Backends without random access
// (FTP) contribute the first chunk only — still a valid pre-filter.
func (s *Scanner) partialHash(ctx context.Context, r db.DupCandidate) (string, error) {
	rc, err := s.fs.ReadFile(ctx, r.OriginalPath)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	h := sha256.New()
	buf := make([]byte, partialChunk)
	n, err := io.ReadFull(rc, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}
	h.Write(buf[:n])
	if ra := randomAccess(rc); ra != nil && r.Size > 2*partialChunk {
		m, err := ra.ReadAt(buf, r.Size-partialChunk)
		if err != nil && err != io.EOF {
			return "", err
		}
		h.Write(buf[:m])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fullHash streams the whole file through sha256.
func (s *Scanner) fullHash(ctx context.Context, r db.DupCandidate) (string, error) {
	rc, err := s.fs.ReadFile(ctx, r.OriginalPath)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	h := sha256.New()
	n, err := io.Copy(h, ctxReader{ctx: ctx, r: rc})
	if err != nil {
		return "", err
	}
	if n != r.Size {
		return "", fmt.Errorf("size changed: read %d bytes, scanned %d", n, r.Size)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ctxReader stops a long hash read on cancellation.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

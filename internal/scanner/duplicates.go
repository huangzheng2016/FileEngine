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
	"sync/atomic"
	"time"

	"FileEngine/internal/db"
)

const (
	partialChunk = 64 << 10
	// go-smb2 sends one request per Read call, so small reads cost a round
	// trip each.
	hashBufSize = 1 << 20
)

// Each DecideBatch schedules a full-session count refresh, so decisions are
// written in large batches rather than per bucket.
var (
	dupPage          = 5000
	decideBatchSize  = 5000
	decideBatchEvery = 30 * time.Second
)

// Each group's original is the oldest file (then shortest path, path, id).
// Full hashes of unchanged files are reused; unreadable files are skipped,
// never fatal.
func (s *Scanner) detectDuplicates(ctx context.Context, sessionID uint, enabled bool) error {
	if err := s.repo.ResetDuplicates(sessionID, !enabled); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	pass, cancel := context.WithCancel(ctx)
	defer cancel()
	w := newWriter(s.repo, cancel)

	pool := s.newHashPool()
	dec := s.newDecider(sessionID, cancel)
	var groups, buckets atomic.Int64
	// Bounds the buckets held in memory while their hashes are pending.
	sem := make(chan struct{}, 4*s.readConcurrency())
	var coord sync.WaitGroup
	launch := func(rows []db.DupCandidate) {
		buckets.Add(1)
		sem <- struct{}{}
		coord.Add(1)
		go func() {
			defer func() { <-sem; coord.Done() }()
			n, ds := s.dedupeBucket(pass, rows, pool, w)
			groups.Add(int64(n))
			dec.add(ds)
		}()
	}

	var readErr error
	var cur []db.DupCandidate
	var afterSize int64
	var afterID uint
	for pass.Err() == nil {
		rows, err := s.repo.FilesBySizeAfter(sessionID, afterSize, afterID, dupPage)
		if err != nil {
			readErr = err
			break
		}
		for _, r := range rows {
			// A bucket can span pages; it is complete once the size changes.
			if len(cur) > 0 && cur[0].Size != r.Size {
				launch(cur)
				cur = nil
			}
			cur = append(cur, r)
		}
		if len(rows) < dupPage {
			if len(cur) > 0 {
				launch(cur)
			}
			break
		}
		last := rows[len(rows)-1]
		afterSize, afterID = last.Size, last.ID
	}
	coord.Wait()
	pool.close()
	decErr := dec.close(pass.Err() == nil)
	if err := w.close(); err != nil {
		return err
	}
	if decErr != nil {
		return decErr
	}
	if readErr != nil {
		return readErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	log.Printf("scan session %d: %d duplicate groups across %d size buckets", sessionID, groups.Load(), buckets.Load())
	return nil
}

func (s *Scanner) dedupeBucket(ctx context.Context, rows []db.DupCandidate, pool *hashPool, w *scanWriter) (int, []db.Decision) {
	size := rows[0].Size
	var need []int // indexes of rows without a stored full hash
	for i, r := range rows {
		if r.Hash == "" {
			need = append(need, i)
		}
	}
	full := func(idx []int) {
		hashes := pool.hashAll(ctx, rows, idx, true)
		for _, i := range idx {
			if h := hashes[i]; h != "" {
				rows[i].Hash = h
				w.send(writeBatch{updates: []db.EntryUpdate{{ID: rows[i].ID, Cols: map[string]any{"hash": h}}}})
			}
		}
	}
	switch {
	case len(need) == 0:
	case len(need) < len(rows) || size <= 2*partialChunk:
		// Stored hashes are full hashes, and small files read whole anyway:
		// a partial pass would save nothing.
		full(need)
	default:
		partial := pool.hashAll(ctx, rows, need, false)
		byPartial := map[string][]int{}
		for _, i := range need {
			if h := partial[i]; h != "" {
				byPartial[h] = append(byPartial[h], i)
			}
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
	var decisions []db.Decision
	for _, g := range byHash {
		if len(g) < 2 {
			continue
		}
		groups++
		sort.Slice(g, func(i, j int) bool { return originalFirst(g[i], g[j]) })
		orig := g[0].ID
		for _, d := range g[1:] {
			w.send(writeBatch{updates: []db.EntryUpdate{{ID: d.ID, Cols: map[string]any{"duplicate_of": orig}}}})
			if d.Role == db.RoleUndecided {
				decisions = append(decisions, db.Decision{Path: d.OriginalPath, Role: db.RoleDuplicate,
					Reason: "内容与「" + g[0].OriginalPath + "」重复"})
			}
		}
	}
	return groups, decisions
}

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

// decider collects the duplicate decisions of finished buckets and writes
// them through db.DecideBatch, the only decision writer.
type decider struct {
	s         *Scanner
	sessionID uint
	ch        chan []db.Decision
	done      chan struct{}
	onErr     func()
	err       error
	write     atomic.Bool // false once the final flush must be skipped
}

func (s *Scanner) newDecider(sessionID uint, onErr func()) *decider {
	d := &decider{s: s, sessionID: sessionID, ch: make(chan []db.Decision, 64), done: make(chan struct{}), onErr: onErr}
	d.write.Store(true)
	go d.loop()
	return d
}

func (d *decider) add(ds []db.Decision) {
	if len(ds) > 0 {
		d.ch <- ds
	}
}

func (d *decider) loop() {
	defer close(d.done)
	var pending []db.Decision
	last := time.Now()
	flush := func() {
		if len(pending) == 0 || d.err != nil {
			return
		}
		if _, err := d.s.repo.DecideBatch(d.sessionID, pending); err != nil {
			d.err = err
			d.onErr()
		}
		pending = nil
		last = time.Now()
	}
	for ds := range d.ch {
		pending = append(pending, ds...)
		if len(pending) >= decideBatchSize || time.Since(last) >= decideBatchEvery {
			flush()
		}
	}
	if d.write.Load() {
		flush()
	}
}

// close waits for the decisions sent so far; the remainder is written only
// when final is set (a cancelled pass leaves it to the next scan).
func (d *decider) close(final bool) error {
	d.write.Store(final)
	close(d.ch)
	<-d.done
	return d.err
}

// hashPool reads files for every bucket on one set of workers, so the
// backend's read concurrency is used even when most buckets hold two files.
type hashPool struct {
	s    *Scanner
	jobs chan hashJob
	wg   sync.WaitGroup
}

type hashJob struct {
	ctx  context.Context
	row  db.DupCandidate
	full bool
	out  *string
	wg   *sync.WaitGroup
}

func (s *Scanner) newHashPool() *hashPool {
	p := &hashPool{s: s, jobs: make(chan hashJob)}
	for range s.readConcurrency() {
		p.wg.Add(1)
		go p.work()
	}
	return p
}

func (p *hashPool) work() {
	defer p.wg.Done()
	buf := make([]byte, hashBufSize)
	for j := range p.jobs {
		if j.ctx.Err() == nil {
			p.s.progress.setPath(j.row.OriginalPath)
			var h string
			var err error
			if j.full {
				h, err = p.s.fullHash(j.ctx, j.row, buf)
			} else {
				h, err = p.s.partialHash(j.ctx, j.row, buf[:partialChunk])
			}
			if err != nil {
				if j.ctx.Err() == nil {
					log.Printf("warn: duplicates: hash %s: %v", j.row.OriginalPath, err)
				}
			} else {
				*j.out = h
			}
		}
		j.wg.Done()
	}
}

// hashAll returns the hashes indexed like rows; "" where a file could not be
// read.
func (p *hashPool) hashAll(ctx context.Context, rows []db.DupCandidate, idx []int, full bool) []string {
	out := make([]string, len(rows))
	var wg sync.WaitGroup
	for _, i := range idx {
		wg.Add(1)
		p.jobs <- hashJob{ctx: ctx, row: rows[i], full: full, out: &out[i], wg: &wg}
	}
	wg.Wait()
	return out
}

// Every bucket must be done submitting.
func (p *hashPool) close() {
	close(p.jobs)
	p.wg.Wait()
}

// Backends without random access (FTP) contribute the first chunk only,
// still a valid pre-filter.
func (s *Scanner) partialHash(ctx context.Context, r db.DupCandidate, buf []byte) (string, error) {
	rc, err := s.fs.ReadFile(ctx, r.OriginalPath)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	h := sha256.New()
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

func (s *Scanner) fullHash(ctx context.Context, r db.DupCandidate, buf []byte) (string, error) {
	rc, err := s.fs.ReadFile(ctx, r.OriginalPath)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(h, ctxReader{ctx: ctx, r: rc}, buf)
	if err != nil {
		return "", err
	}
	if n != r.Size {
		return "", fmt.Errorf("size changed: read %d bytes, scanned %d", n, r.Size)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Also hides any WriterTo of the file, so io.CopyBuffer uses its buffer.
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

package scanner

import (
	"sync/atomic"

	"FileEngine/internal/db"
)

// Large enough that commit fsyncs stay rare (~100ms each on bind-mounted
// volumes), small enough that memory stays bounded on million-entry trees.
var commitRows = 20000

type writeBatch struct {
	inserts []db.FileEntry
	updates []db.EntryUpdate
}

func (b writeBatch) empty() bool { return len(b.inserts) == 0 && len(b.updates) == 0 }

// SQLite has one writer connection, so producers never write to the DB
// themselves; the bounded channel gives them backpressure.
type scanWriter struct {
	repo    *db.Repository
	ch      chan writeBatch
	done    chan struct{}
	onErr   func()
	err     error
	commits int
	failed  atomic.Bool

	inserts []db.FileEntry
	updates []db.EntryUpdate
}

// onErr runs once on the first write failure so the producers stop early.
func newWriter(repo *db.Repository, onErr func()) *scanWriter {
	w := &scanWriter{repo: repo, ch: make(chan writeBatch, 64), done: make(chan struct{}), onErr: onErr}
	go w.loop()
	return w
}

func (w *scanWriter) send(b writeBatch) {
	if b.empty() || w.failed.Load() {
		return
	}
	w.ch <- b
}

func (w *scanWriter) loop() {
	defer close(w.done)
	for b := range w.ch {
		if w.err != nil {
			continue // drain after a failure
		}
		w.inserts = append(w.inserts, b.inserts...)
		w.updates = append(w.updates, b.updates...)
		if len(w.inserts)+len(w.updates) >= commitRows {
			w.flush()
		}
	}
	if w.err == nil {
		w.flush()
	}
}

func (w *scanWriter) flush() {
	if len(w.inserts) == 0 && len(w.updates) == 0 {
		return
	}
	err := db.WithRetry(func() error { return w.repo.ApplyScanBatch(w.inserts, w.updates) })
	if err != nil {
		w.err = err
		w.failed.Store(true)
		if w.onErr != nil {
			w.onErr()
		}
		return
	}
	w.commits++
	w.inserts = w.inserts[:0]
	w.updates = w.updates[:0]
}

// Every producer must be done sending.
func (w *scanWriter) close() error {
	close(w.ch)
	<-w.done
	return w.err
}

package eventfeed

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var ErrClosed = errors.New("event streams are closed")

// Registry owns every open stream (SSE connection or desktop subscription).
// Hertz request contexts are not cancelled at shutdown, so each stream runs on a
// context derived here; Close cancels all of them and waits for them to leave.
type Registry struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
	open   atomic.Int64
}

func NewRegistry() *Registry {
	ctx, cancel := context.WithCancel(context.Background())
	return &Registry{ctx: ctx, cancel: cancel}
}

// Begin registers one stream. The returned context ends when the registry
// closes or when done is called; done must always be called.
func (r *Registry) Begin() (context.Context, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, nil, ErrClosed
	}
	r.wg.Add(1)
	r.open.Add(1)
	ctx, cancel := context.WithCancel(r.ctx)
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			cancel()
			r.open.Add(-1)
			r.wg.Done()
		})
	}, nil
}

// Open is the number of streams that have begun and not yet finished.
func (r *Registry) Open() int { return int(r.open.Load()) }

// Close cancels every stream and waits for them to finish, until ctx ends.
func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.cancel()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

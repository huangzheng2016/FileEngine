package eventfeed

import (
	"context"
	"errors"
	"sync"
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
	ctx, cancel := context.WithCancel(r.ctx)
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			cancel()
			r.wg.Done()
		})
	}, nil
}

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

package agent

import (
	"context"
	"sync"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Worker 0 sends its first request alone; the other workers start once that
// response begins, so tools, system and the shared snapshot are written to the
// provider cache once instead of once per worker.

// warmTimeout bounds the wait when worker 0 never gets a response.
var warmTimeout = 45 * time.Second

type warmGate struct {
	once sync.Once
	ch   chan struct{}
}

func newWarmGate() *warmGate { return &warmGate{ch: make(chan struct{})} }

func (g *warmGate) open() { g.once.Do(func() { close(g.ch) }) }

func (g *warmGate) wait(ctx context.Context) {
	select {
	case <-g.ch:
	case <-ctx.Done():
	case <-time.After(warmTimeout):
	}
}

// warmModel opens the gate when the first response starts (or the first
// request fails, so a broken model never holds the other workers back).
type warmModel struct {
	inner einomodel.ChatModel
	gate  *warmGate
}

func (m *warmModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	msg, err := m.inner.Generate(ctx, input, opts...)
	m.gate.open()
	return msg, err
}

func (m *warmModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := m.inner.Stream(ctx, input, opts...)
	if err != nil {
		m.gate.open()
		return nil, err
	}
	return schema.StreamReaderWithConvert(sr, func(msg *schema.Message) (*schema.Message, error) {
		m.gate.open()
		return msg, nil
	}), nil
}

func (m *warmModel) BindTools(tools []*schema.ToolInfo) error { return m.inner.BindTools(tools) }

package agent

import (
	"context"
	"sync/atomic"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// tokenTrackingModel wraps a ChatModel to accumulate token usage across all Generate calls
// (including intermediate ReAct tool-use rounds).
type tokenTrackingModel struct {
	inner            einomodel.ChatModel
	promptTokens     int64
	completionTokens int64
	totalTokens      int64
}

func newTokenTrackingModel(inner einomodel.ChatModel) *tokenTrackingModel {
	return &tokenTrackingModel{inner: inner}
}

func (m *tokenTrackingModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	result, err := m.inner.Generate(ctx, input, opts...)
	if err == nil && result != nil && result.ResponseMeta != nil && result.ResponseMeta.Usage != nil {
		u := result.ResponseMeta.Usage
		atomic.AddInt64(&m.promptTokens, int64(u.PromptTokens))
		atomic.AddInt64(&m.completionTokens, int64(u.CompletionTokens))
		atomic.AddInt64(&m.totalTokens, int64(u.TotalTokens))
	}
	return result, err
}

func (m *tokenTrackingModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := m.inner.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	// Usage (when the provider reports it) rides on a final frame; count every
	// frame that carries it so streamed calls are not missed.
	return schema.StreamReaderWithConvert(sr, func(msg *schema.Message) (*schema.Message, error) {
		if msg != nil && msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
			u := msg.ResponseMeta.Usage
			atomic.AddInt64(&m.promptTokens, int64(u.PromptTokens))
			atomic.AddInt64(&m.completionTokens, int64(u.CompletionTokens))
			atomic.AddInt64(&m.totalTokens, int64(u.TotalTokens))
		}
		return msg, nil
	}), nil
}

func (m *tokenTrackingModel) BindTools(tools []*schema.ToolInfo) error {
	return m.inner.BindTools(tools)
}

func (m *tokenTrackingModel) Usage() (prompt, completion, total int) {
	return int(atomic.LoadInt64(&m.promptTokens)),
		int(atomic.LoadInt64(&m.completionTokens)),
		int(atomic.LoadInt64(&m.totalTokens))
}

func (m *tokenTrackingModel) ResetUsage() {
	atomic.StoreInt64(&m.promptTokens, 0)
	atomic.StoreInt64(&m.completionTokens, 0)
	atomic.StoreInt64(&m.totalTokens, 0)
}

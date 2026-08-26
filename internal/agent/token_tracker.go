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
	cachedTokens     int64
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
		atomic.AddInt64(&m.cachedTokens, int64(u.PromptTokenDetails.CachedTokens))
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
	// Providers may emit usage on multiple frames per call with cumulative
	// counters (e.g. claude ext: message_start + message_delta). Track per-call
	// state in this closure and add only the delta so cumulative frames are
	// never double counted.
	var lastPrompt, lastCached, lastCompletion, lastTotal int64
	return schema.StreamReaderWithConvert(sr, func(msg *schema.Message) (*schema.Message, error) {
		if msg != nil && msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
			u := msg.ResponseMeta.Usage
			p, ca, c, t := int64(u.PromptTokens), int64(u.PromptTokenDetails.CachedTokens), int64(u.CompletionTokens), int64(u.TotalTokens)
			if p > lastPrompt {
				atomic.AddInt64(&m.promptTokens, p-lastPrompt)
				lastPrompt = p
			}
			if ca > lastCached {
				atomic.AddInt64(&m.cachedTokens, ca-lastCached)
				lastCached = ca
			}
			if c > lastCompletion {
				atomic.AddInt64(&m.completionTokens, c-lastCompletion)
				lastCompletion = c
			}
			if t > lastTotal {
				atomic.AddInt64(&m.totalTokens, t-lastTotal)
				lastTotal = t
			}
		}
		return msg, nil
	}), nil
}

func (m *tokenTrackingModel) BindTools(tools []*schema.ToolInfo) error {
	return m.inner.BindTools(tools)
}

// Usage returns the accumulated counters: prompt tokens (cache hits included),
// cache-hit tokens (a subset of prompt), completion tokens, and total tokens.
func (m *tokenTrackingModel) Usage() (prompt, cached, completion, total int) {
	return int(atomic.LoadInt64(&m.promptTokens)),
		int(atomic.LoadInt64(&m.cachedTokens)),
		int(atomic.LoadInt64(&m.completionTokens)),
		int(atomic.LoadInt64(&m.totalTokens))
}

func (m *tokenTrackingModel) ResetUsage() {
	atomic.StoreInt64(&m.promptTokens, 0)
	atomic.StoreInt64(&m.cachedTokens, 0)
	atomic.StoreInt64(&m.completionTokens, 0)
	atomic.StoreInt64(&m.totalTokens, 0)
}

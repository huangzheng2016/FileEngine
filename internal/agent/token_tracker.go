package agent

import (
	"context"
	"sync/atomic"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/db"
)

// tokenTrackingModel wraps a ChatModel to accumulate token usage across all
// Generate and Stream calls (including intermediate ReAct tool-use rounds).
type tokenTrackingModel struct {
	inner einomodel.ChatModel
	c     *tokenCounters
}

type tokenCounters struct {
	promptTokens     atomic.Int64
	cachedTokens     atomic.Int64
	cacheWriteTokens atomic.Int64
	completionTokens atomic.Int64
	totalTokens      atomic.Int64
}

func newTokenTrackingModel(inner einomodel.ChatModel) *tokenTrackingModel {
	return &tokenTrackingModel{inner: inner, c: &tokenCounters{}}
}

// sibling wraps another model (e.g. the bulk summarization model) into the
// same counters, so its usage lands in the same run totals.
func (m *tokenTrackingModel) sibling(inner einomodel.ChatModel) *tokenTrackingModel {
	return &tokenTrackingModel{inner: inner, c: m.c}
}

func (m *tokenTrackingModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	result, err := m.inner.Generate(ctx, input, opts...)
	if err == nil && result != nil && result.ResponseMeta != nil && result.ResponseMeta.Usage != nil {
		u := result.ResponseMeta.Usage
		m.c.promptTokens.Add(int64(u.PromptTokens))
		m.c.cachedTokens.Add(int64(u.PromptTokenDetails.CachedTokens))
		m.c.cacheWriteTokens.Add(int64(u.PromptTokenDetails.CacheWriteTokens))
		m.c.completionTokens.Add(int64(u.CompletionTokens))
		m.c.totalTokens.Add(int64(u.TotalTokens))
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
	var last [5]int64
	counters := [5]*atomic.Int64{&m.c.promptTokens, &m.c.cachedTokens, &m.c.cacheWriteTokens, &m.c.completionTokens, &m.c.totalTokens}
	return schema.StreamReaderWithConvert(sr, func(msg *schema.Message) (*schema.Message, error) {
		if msg != nil && msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
			u := msg.ResponseMeta.Usage
			cur := [5]int64{int64(u.PromptTokens), int64(u.PromptTokenDetails.CachedTokens),
				int64(u.PromptTokenDetails.CacheWriteTokens), int64(u.CompletionTokens), int64(u.TotalTokens)}
			for i := range cur {
				if cur[i] > last[i] {
					counters[i].Add(cur[i] - last[i])
					last[i] = cur[i]
				}
			}
		}
		return msg, nil
	}), nil
}

func (m *tokenTrackingModel) BindTools(tools []*schema.ToolInfo) error {
	return m.inner.BindTools(tools)
}

// Usage returns the accumulated counters: prompt tokens (cache hits and
// writes included), cache-hit and cache-write subsets of prompt, completion
// and total tokens.
func (m *tokenTrackingModel) Usage() db.TokenCounts {
	return db.TokenCounts{
		Prompt:     int(m.c.promptTokens.Load()),
		Cached:     int(m.c.cachedTokens.Load()),
		CacheWrite: int(m.c.cacheWriteTokens.Load()),
		Completion: int(m.c.completionTokens.Load()),
		Total:      int(m.c.totalTokens.Load()),
	}
}

func (m *tokenTrackingModel) ResetUsage() {
	m.c.promptTokens.Store(0)
	m.c.cachedTokens.Store(0)
	m.c.cacheWriteTokens.Store(0)
	m.c.completionTokens.Store(0)
	m.c.totalTokens.Store(0)
}

package agent

import (
	"context"
	"io"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fakeStreamModel struct {
	frames []*schema.Message
}

func (f *fakeStreamModel) Generate(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	return nil, nil
}

func (f *fakeStreamModel) Stream(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](len(f.frames))
	go func() {
		for _, frame := range f.frames {
			sw.Send(frame, nil)
		}
		sw.Close()
	}()
	return sr, nil
}

func (f *fakeStreamModel) BindTools(_ []*schema.ToolInfo) error { return nil }

func drainStream(t *testing.T, sr *schema.StreamReader[*schema.Message]) {
	t.Helper()
	defer sr.Close()
	for {
		_, err := sr.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
	}
}

// claudeTwoFrameUsage mimics eino-ext claude v0.1.25: a message_start frame
// with initial usage followed by a message_delta frame whose counters are
// cumulative for the whole call. CachedTokens rides the same cumulative shape.
func claudeTwoFrameUsage() []*schema.Message {
	return []*schema.Message{
		{
			Role:    schema.Assistant,
			Content: "chunk-1",
			ResponseMeta: &schema.ResponseMeta{
				Usage: &schema.TokenUsage{PromptTokens: 100, PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 40}, CompletionTokens: 1, TotalTokens: 101},
			},
		},
		{
			Role:    schema.Assistant,
			Content: "chunk-2",
			ResponseMeta: &schema.ResponseMeta{
				FinishReason: "stop",
				Usage:        &schema.TokenUsage{PromptTokens: 100, PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 40}, CompletionTokens: 50, TotalTokens: 150},
			},
		},
	}
}

func TestTokenTrackingStreamCumulativeFrames(t *testing.T) {
	tracker := newTokenTrackingModel(&fakeStreamModel{frames: claudeTwoFrameUsage()})

	sr, err := tracker.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drainStream(t, sr)

	u := tracker.Usage()
	p, ca, c, tt := u.Prompt, u.Cached, u.Completion, u.Total
	if p != 100 || ca != 40 || c != 50 || tt != 150 {
		t.Fatalf("cumulative frames double counted: got (%d, %d, %d, %d), want (100, 40, 50, 150)", p, ca, c, tt)
	}
}

func TestTokenTrackingStreamAccumulatesAcrossCalls(t *testing.T) {
	tracker := newTokenTrackingModel(&fakeStreamModel{frames: claudeTwoFrameUsage()})

	for i := 0; i < 2; i++ {
		sr, err := tracker.Stream(context.Background(), nil)
		if err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
		drainStream(t, sr)
	}

	u := tracker.Usage()
	p, ca, c, tt := u.Prompt, u.Cached, u.Completion, u.Total
	if p != 200 || ca != 80 || c != 100 || tt != 300 {
		t.Fatalf("two calls: got (%d, %d, %d, %d), want (200, 80, 100, 300)", p, ca, c, tt)
	}
}

func TestTokenTrackingStreamFramesWithoutUsage(t *testing.T) {
	frames := []*schema.Message{
		{Role: schema.Assistant, Content: "no-usage"},
		nil,
		{Role: schema.Assistant, Content: "still-no-usage", ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}},
	}
	tracker := newTokenTrackingModel(&fakeStreamModel{frames: frames})

	sr, err := tracker.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drainStream(t, sr)

	u := tracker.Usage()
	p, ca, c, tt := u.Prompt, u.Cached, u.Completion, u.Total
	if p != 0 || ca != 0 || c != 0 || tt != 0 {
		t.Fatalf("frames without usage: got (%d, %d, %d, %d), want (0, 0, 0, 0)", p, ca, c, tt)
	}
}

func TestTokenTrackingGenerateWithCachedTokens(t *testing.T) {
	inner := &fakeStreamModel{}
	tracker := newTokenTrackingModel(inner)
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: "done",
		ResponseMeta: &schema.ResponseMeta{
			Usage: &schema.TokenUsage{PromptTokens: 100, PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 40}, CompletionTokens: 50, TotalTokens: 150},
		},
	}
	g := &fakeGenerateModel{msg: msg}
	tracker = newTokenTrackingModel(g)
	if _, err := tracker.Generate(context.Background(), nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	u := tracker.Usage()
	p, ca, c, tt := u.Prompt, u.Cached, u.Completion, u.Total
	if p != 100 || ca != 40 || c != 50 || tt != 150 {
		t.Fatalf("generate with cached: got (%d, %d, %d, %d), want (100, 40, 50, 150)", p, ca, c, tt)
	}
}

type fakeGenerateModel struct{ msg *schema.Message }

func (f *fakeGenerateModel) Generate(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	return f.msg, nil
}
func (f *fakeGenerateModel) Stream(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}
func (f *fakeGenerateModel) BindTools(_ []*schema.ToolInfo) error { return nil }

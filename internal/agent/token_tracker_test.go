package agent

import (
	"context"
	"io"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeStreamModel replays a fixed sequence of stream frames.
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
// cumulative for the whole call.
func claudeTwoFrameUsage() []*schema.Message {
	return []*schema.Message{
		{
			Role:    schema.Assistant,
			Content: "chunk-1",
			ResponseMeta: &schema.ResponseMeta{
				Usage: &schema.TokenUsage{PromptTokens: 100, CompletionTokens: 1, TotalTokens: 101},
			},
		},
		{
			Role:    schema.Assistant,
			Content: "chunk-2",
			ResponseMeta: &schema.ResponseMeta{
				FinishReason: "stop",
				Usage:        &schema.TokenUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
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

	p, c, tt := tracker.Usage()
	if p != 100 || c != 50 || tt != 150 {
		t.Fatalf("cumulative frames double counted: got (%d, %d, %d), want (100, 50, 150)", p, c, tt)
	}
}

func TestTokenTrackingStreamAccumulatesAcrossCalls(t *testing.T) {
	// Two separate calls on the same tracker must both count in full:
	// the delta state is per-call, not shared.
	tracker := newTokenTrackingModel(&fakeStreamModel{frames: claudeTwoFrameUsage()})

	for i := 0; i < 2; i++ {
		sr, err := tracker.Stream(context.Background(), nil)
		if err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
		drainStream(t, sr)
	}

	p, c, tt := tracker.Usage()
	if p != 200 || c != 100 || tt != 300 {
		t.Fatalf("two calls: got (%d, %d, %d), want (200, 100, 300)", p, c, tt)
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

	p, c, tt := tracker.Usage()
	if p != 0 || c != 0 || tt != 0 {
		t.Fatalf("frames without usage: got (%d, %d, %d), want (0, 0, 0)", p, c, tt)
	}
}

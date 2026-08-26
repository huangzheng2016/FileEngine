package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/db"
)

// scriptedModel answers the first Generate call with a tool call and the
// second with the final assistant message.
type scriptedModel struct {
	calls int
}

func (m *scriptedModel) Generate(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.calls++
	if m.calls == 1 {
		return &schema.Message{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: schema.FunctionCall{Name: "echo", Arguments: `{"text":"hi"}`},
			}},
		}, nil
	}
	return &schema.Message{Role: schema.Assistant, Content: "done"}, nil
}

func (m *scriptedModel) Stream(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("streaming not supported by scriptedModel")
}

func (m *scriptedModel) BindTools(_ []*schema.ToolInfo) error { return nil }

type echoInput struct {
	Text string `json:"text" jsonschema_description:"text to echo"`
}

type echoOutput struct {
	Text string `json:"text"`
}

func TestRunADKOnceForwardsStepEvents(t *testing.T) {
	ctx := context.Background()

	echoTool, err := utils.InferTool("echo", "echo back the text", func(_ context.Context, in *echoInput) (*echoOutput, error) {
		return &echoOutput{Text: in.Text}, nil
	})
	if err != nil {
		t.Fatalf("build echo tool: %v", err)
	}

	adkAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:  "test-agent",
		Model: &scriptedModel{},
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{echoTool}},
		},
		MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	logger := NewLogger(nil, 200, 5)

	ch := db.SubscribeEvents(200)
	defer db.UnsubscribeEvents(200, ch)

	msg, err := runADKOnce(ctx, adkAgent, []*schema.Message{{Role: schema.User, Content: "go"}}, logger)
	if err != nil {
		t.Fatalf("runADKOnce: %v", err)
	}
	if msg == nil || msg.Content != "done" {
		t.Fatalf("final message: %+v", msg)
	}

	// 1st event: tool_call for echo
	ev := recvBusEvent(t, ch)
	p1, ok := ev.Payload.(db.ToolCallPayload)
	if ev.Type != db.EventToolCall || !ok {
		t.Fatalf("event 1: type=%q payload=%+v", ev.Type, ev.Payload)
	}
	if p1.WorkerID != 5 || p1.ToolName != "echo" || p1.Input != `{"text":"hi"}` {
		t.Fatalf("tool_call payload: %+v", p1)
	}

	// 2nd event: tool_result for echo
	ev = recvBusEvent(t, ch)
	p2, ok := ev.Payload.(db.ToolResultPayload)
	if ev.Type != db.EventToolResult || !ok {
		t.Fatalf("event 2: type=%q payload=%+v", ev.Type, ev.Payload)
	}
	if p2.WorkerID != 5 || p2.ToolName != "echo" || !p2.OK || p2.Output == "" {
		t.Fatalf("tool_result payload: %+v", p2)
	}

	// The final assistant message is NOT emitted by runADKOnce — the caller
	// logs it via LogMessage with token usage.
	select {
	case ev := <-ch:
		t.Fatalf("unexpected extra event: %s %+v", ev.Type, ev.Payload)
	case <-time.After(100 * time.Millisecond):
	}
}

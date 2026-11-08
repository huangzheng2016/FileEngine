package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/db"
)

func runADKWithRetry(ctx context.Context, agent *adk.ChatModelAgent, messages []*schema.Message, logger *Logger, label string, tracker *tokenTrackingModel) (*schema.Message, error) {
	attempt := func(ctx context.Context) (*schema.Message, error) {
		callCtx, callCancel := withRunTimeout(ctx)
		defer callCancel()
		tracker.ResetUsage()
		defer func() { logger.AddUsage(tracker.Usage()) }()
		return runADKOnce(callCtx, agent, messages, logger)
	}
	return runWithRetry(ctx, attempt, logger, label)
}

// runADKOnce executes one full streaming agent run and forwards it to the
// session stream:
//   - assistant output streams as `reasoning` / `text` deltas under one
//     msg_id, then a `message` (role assistant, full content) finalizes it;
//   - each tool call of that message becomes a `tool_call` (call_id = the
//     model's ToolCall.ID, generated when empty; args parsed to JSON);
//   - each tool result becomes a `tool_result` matched by call_id, with ok /
//     error derived from the tools' `success:false` convention and the
//     call→result duration.
//
// It returns the last assistant message of the run.
func runADKOnce(ctx context.Context, agent *adk.ChatModelAgent, messages []*schema.Message, logger *Logger) (*schema.Message, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true})
	iterator := runner.Run(ctx, messages)
	drain := func() {
		for {
			if _, ok := iterator.Next(); !ok {
				return
			}
		}
	}

	calls := &callTracker{logger: logger}
	var lastAssistantMsg *schema.Message
	fail := func(err error) (*schema.Message, error) {
		drain()
		if lastAssistantMsg != nil {
			return nil, &respondedError{err: err}
		}
		return nil, err
	}
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return fail(event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		mo := event.Output.MessageOutput
		switch mo.Role {
		case schema.Assistant:
			msg, err := streamAssistant(mo, logger)
			var willRetry *adk.WillRetryError
			if errors.As(err, &willRetry) {
				// The request failed and ADK re-issues it; its replacement
				// arrives as the next assistant event.
				logger.LogMessage("system", fmt.Sprintf("模型请求失败，正在重试：%s", willRetry.ErrStr))
				continue
			}
			if err != nil {
				return fail(err)
			}
			lastAssistantMsg = msg
			for _, tc := range msg.ToolCalls {
				calls.call(tc)
			}
		case schema.Tool:
			msg, err := mo.GetMessage()
			if err != nil {
				return fail(err)
			}
			if msg != nil {
				calls.result(mo.ToolName, msg)
			}
		}
	}
	return lastAssistantMsg, nil
}

// A stream that breaks midway is still finalized with the content received
// so far.
func streamAssistant(mo *adk.MessageVariant, logger *Logger) (*schema.Message, error) {
	msgID := logger.NewMsgID()
	if !mo.IsStreaming {
		msg := mo.Message
		if msg == nil {
			msg = &schema.Message{Role: schema.Assistant}
		}
		logger.EmitDelta(db.EvReasoning, msgID, msg.ReasoningContent)
		if msg.Content != "" || msg.ReasoningContent != "" {
			logger.EmitMessage(msgID, "assistant", msg.Content)
		}
		return msg, nil
	}

	stream := mo.MessageStream
	defer stream.Close()
	var chunks []*schema.Message
	var content strings.Builder
	streamed := false
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if streamed {
				logger.EmitMessage(msgID, "assistant", content.String())
			}
			return nil, err
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		if chunk.ReasoningContent != "" {
			logger.EmitDelta(db.EvReasoning, msgID, chunk.ReasoningContent)
			streamed = true
		}
		if chunk.Content != "" {
			logger.EmitDelta(db.EvText, msgID, chunk.Content)
			content.WriteString(chunk.Content)
			streamed = true
		}
	}
	msg := &schema.Message{Role: schema.Assistant}
	if len(chunks) > 0 {
		concat, err := schema.ConcatMessages(chunks)
		if err != nil {
			return nil, fmt.Errorf("concat assistant stream: %w", err)
		}
		msg = concat
	}
	if streamed || msg.Content != "" {
		logger.EmitMessage(msgID, "assistant", msg.Content)
	}
	return msg, nil
}

type pendingCall struct {
	id    string
	name  string
	start time.Time
}

type callTracker struct {
	logger  *Logger
	pending []pendingCall
}

func (t *callTracker) call(tc schema.ToolCall) {
	id := tc.ID
	if id == "" {
		id = t.logger.nextID("call")
	}
	t.pending = append(t.pending, pendingCall{id: id, name: tc.Function.Name, start: time.Now()})
	t.logger.EmitToolCall(id, tc.Function.Name, parseJSONOrRaw(tc.Function.Arguments))
}

// match removes and returns the pending call of a result: by call id when
// the result carries one, else the oldest pending call of the same tool
// (calls whose id was generated here).
func (t *callTracker) match(callID, name string) pendingCall {
	for i, p := range t.pending {
		if (callID != "" && p.id == callID) || (callID == "" && p.name == name) {
			t.pending = append(t.pending[:i], t.pending[i+1:]...)
			return p
		}
	}
	if callID == "" {
		callID = t.logger.nextID("call")
	}
	return pendingCall{id: callID, name: name, start: time.Now()}
}

func (t *callTracker) result(toolName string, msg *schema.Message) {
	if toolName == "" {
		toolName = msg.ToolName
	}
	p := t.match(msg.ToolCallID, toolName)
	raw := msg.Content
	// Enhanced tools (view_image) return multimodal parts with empty Content;
	// emit a text stand-in — base64 image data never enters the stream.
	if raw == "" && len(msg.UserInputMultiContent) > 0 {
		raw = describeMessageParts(msg.UserInputMultiContent)
	}
	output := parseJSONOrRaw(raw)
	ok, errMsg := toolOutcome(output)
	t.logger.EmitToolResult(db.ToolResultData{
		CallID: p.id, Name: p.name, OK: ok, Output: output, Error: errMsg,
		DurationMs: time.Since(p.start).Milliseconds(),
	})
}

func parseJSONOrRaw(s string) any {
	if strings.TrimSpace(s) == "" {
		return s
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	return v
}

// toolOutcome applies the tools' result convention: agent-recoverable
// rejections are results carrying a non-empty `error` and/or
// `"success": false`.
func toolOutcome(output any) (bool, string) {
	m, isObj := output.(map[string]any)
	if !isObj {
		return true, ""
	}
	errMsg, _ := m["error"].(string)
	if success, has := m["success"].(bool); (has && !success) || errMsg != "" {
		return false, errMsg
	}
	return true, ""
}

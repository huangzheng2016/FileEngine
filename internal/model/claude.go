package model

import (
	"context"
	"encoding/json"
	"fmt"

	claude "github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// claudeModel adds what the eino claude model lacks:
//   - the rolling cache breakpoint on the last message. eino only places it
//     itself when no message carries an explicit mark, and the shared-context
//     message always does, so without it no request ever caches its history.
//     Tools, system, shared context and the last message are the four
//     breakpoints the API allows.
//   - Generate on top of the streaming endpoint, which the API requires for
//     large max_tokens; non-streaming callers (model test, reflection,
//     summarization) work with any output ceiling.
//
// Thinking blocks are not replayed in tool loops: eino sends a block only when
// its text is non-empty, which never holds with display "omitted" (the default
// on current models), and it keeps one block per message. A faithful replay
// also conflicts with clearing old tool results, because the API rejects a
// history whose earlier content changed once thinking blocks follow it.
type claudeModel struct {
	inner *claude.ChatModel
}

// Mirrors the unexported key of the eino claude package; a test pins it.
const claudeBreakpointKey = "_eino_claude_breakpoint"

// With the automatic tools and system marks, two explicit message marks are
// the most that fit in the API's limit of four.
const maxExplicitMessageMarks = 2

func hasBreakpoint(m *schema.Message) bool {
	marked, _ := m.Extra[claudeBreakpointKey].(bool)
	return marked
}

// withTailBreakpoint returns input with the last message marked; input and its
// messages are not modified.
func withTailBreakpoint(input []*schema.Message) []*schema.Message {
	if len(input) == 0 {
		return input
	}
	last := input[len(input)-1]
	if last == nil || last.Role == schema.System || hasBreakpoint(last) {
		return input
	}
	marked := 0
	for _, m := range input {
		if m != nil && hasBreakpoint(m) {
			marked++
		}
	}
	if marked >= maxExplicitMessageMarks {
		return input
	}
	out := make([]*schema.Message, len(input))
	copy(out, input)
	out[len(out)-1] = claude.SetMessageCacheControl(last, nil)
	return out
}

func (m *claudeModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.inner.Stream(ctx, withTailBreakpoint(withValidToolArguments(input)), opts...)
}

// The client embeds tool-call arguments as raw JSON, so one truncated call in
// history (a stream cut at max_tokens) would make every later request body
// invalid. Such arguments are replayed as an empty object; the tool already
// answered that call with an argument error.
func withValidToolArguments(input []*schema.Message) []*schema.Message {
	var out []*schema.Message
	for i, m := range input {
		if m == nil || len(m.ToolCalls) == 0 {
			continue
		}
		var calls []schema.ToolCall
		for j, tc := range m.ToolCalls {
			if tc.Function.Arguments == "" || json.Valid([]byte(tc.Function.Arguments)) {
				continue
			}
			if calls == nil {
				calls = append([]schema.ToolCall(nil), m.ToolCalls...)
			}
			calls[j].Function.Arguments = "{}"
		}
		if calls == nil {
			continue
		}
		if out == nil {
			out = append([]*schema.Message(nil), input...)
		}
		cp := *m
		cp.ToolCalls = calls
		out[i] = &cp
	}
	if out == nil {
		return input
	}
	return out
}

func (m *claudeModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	sr, err := m.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	msg, err := schema.ConcatMessageStream(sr)
	if err != nil {
		return nil, fmt.Errorf("concat claude stream: %w", err)
	}
	return msg, nil
}

func (m *claudeModel) BindTools(tools []*schema.ToolInfo) error {
	return m.inner.BindTools(tools)
}

func (m *claudeModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	cm, ok := inner.(*claude.ChatModel)
	if !ok {
		return nil, fmt.Errorf("unexpected claude model type %T", inner)
	}
	return &claudeModel{inner: cm}, nil
}

func (m *claudeModel) GetType() string { return m.inner.GetType() }

func (m *claudeModel) IsCallbacksEnabled() bool { return m.inner.IsCallbacksEnabled() }

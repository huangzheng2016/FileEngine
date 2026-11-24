package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"FileEngine/internal/db"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// Envelope is embedded first in every tool output, so every result starts
// with {"success": bool, "error"?, "hint"?} followed by the tool's own data.
type Envelope struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

func (e *Envelope) env() *Envelope { return e }

type enveloped interface{ env() *Envelope }

// failed builds a success:false result of any tool output type.
func failed[O any](msg string, hint ...string) *O {
	o := new(O)
	e := any(o).(enveloped).env()
	e.Error = msg
	if len(hint) > 0 {
		e.Hint = hint[0]
	}
	return o
}

func succeeded[O any]() *O {
	o := new(O)
	any(o).(enveloped).env().Success = true
	return o
}

// A rejection is the model's to fix: eino makes any Go error fatal to the
// ReAct loop, so handlers return these as success:false results instead.
type rejectError struct{ msg, hint string }

func (e *rejectError) Error() string { return e.msg }

func rejectf(format string, args ...interface{}) error {
	return &rejectError{msg: fmt.Sprintf(format, args...)}
}

func rejectHint(hint, format string, args ...interface{}) error {
	return &rejectError{msg: fmt.Sprintf(format, args...), hint: hint}
}

func asRejection(err error) (*Envelope, bool) {
	var rj *rejectError
	if errors.As(err, &rj) {
		return &Envelope{Error: rj.msg, Hint: rj.hint}, true
	}
	var dr *db.Rejection
	if errors.As(err, &dr) {
		return &Envelope{Error: dr.Msg}, true
	}
	return nil, false
}

type envTool[I, O any] struct {
	info *schema.ToolInfo
	fn   func(context.Context, *I) (*O, error)
}

func newEnvTool[I, O any](name, desc string, fn func(context.Context, *I) (*O, error)) (tool.BaseTool, error) {
	if _, ok := any(new(O)).(enveloped); !ok {
		return nil, fmt.Errorf("tool %s: output type must embed Envelope", name)
	}
	info, err := utils.GoStruct2ToolInfo[I](name, desc)
	if err != nil {
		return nil, fmt.Errorf("tool %s: %w", name, err)
	}
	return &envTool[I, O]{info: info, fn: fn}, nil
}

func (t *envTool[I, O]) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

func (t *envTool[I, O]) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	in := new(I)
	if err := decodeToolArgs(args, in); err != nil {
		return marshalEnvelope(&Envelope{Error: "工具参数不合法：" + err.Error(), Hint: "按工具说明里的参数格式重新调用"}), nil
	}
	out, err := t.fn(ctx, in)
	if err != nil {
		if env, ok := asRejection(err); ok {
			return marshalEnvelope(env), nil
		}
		return "", err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encode %s result: %w", t.info.Name, err)
	}
	return string(raw), nil
}

// Unknown fields are errors: the schema is not strict on the wire, so a
// misspelled optional field would otherwise be silently ignored.
func decodeToolArgs(args string, dst any) error {
	if len(bytes.TrimSpace([]byte(args))) == 0 {
		args = "{}"
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("JSON 后面有多余内容")
	}
	return nil
}

func marshalEnvelope(e *Envelope) string {
	raw, _ := json.Marshal(e)
	return string(raw)
}

// unknownToolHandler answers a hallucinated tool name with a result the model
// can act on (eino ToolsNodeConfig.UnknownToolsHandler).
func unknownToolHandler(_ context.Context, name, _ string) (string, error) {
	return marshalEnvelope(&Envelope{
		Error: fmt.Sprintf("没有名为 %q 的工具", SanitizeUntrustedName(name)),
		Hint:  "只能调用工具列表里的工具",
	}), nil
}

// envImageTool is the multimodal counterpart: failures are a text part with
// the same envelope, so they read like every other tool result.
type envImageTool[I any] struct {
	info *schema.ToolInfo
	fn   func(context.Context, *I) (*schema.ToolResult, error)
}

func newEnvImageTool[I any](name, desc string, fn func(context.Context, *I) (*schema.ToolResult, error)) (tool.BaseTool, error) {
	info, err := utils.GoStruct2ToolInfo[I](name, desc)
	if err != nil {
		return nil, fmt.Errorf("tool %s: %w", name, err)
	}
	return &envImageTool[I]{info: info, fn: fn}, nil
}

func (t *envImageTool[I]) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

func textResult(s string) *schema.ToolResult {
	return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: s}}}
}

func (t *envImageTool[I]) InvokableRun(ctx context.Context, arg *schema.ToolArgument, _ ...tool.Option) (*schema.ToolResult, error) {
	in := new(I)
	text := ""
	if arg != nil {
		text = arg.Text
	}
	if err := decodeToolArgs(text, in); err != nil {
		return textResult(marshalEnvelope(&Envelope{Error: "工具参数不合法：" + err.Error(), Hint: "按工具说明里的参数格式重新调用"})), nil
	}
	res, err := t.fn(ctx, in)
	if err != nil {
		if env, ok := asRejection(err); ok {
			return textResult(marshalEnvelope(env)), nil
		}
		return nil, err
	}
	return res, nil
}

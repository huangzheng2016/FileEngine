package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
)

// ASCII text is about 4 chars per token, CJK about 1.5.
func estimateTokens(s string) int64 {
	var ascii, nonAscii int
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			nonAscii++
		}
	}
	return int64(float64(ascii)/4.0 + float64(nonAscii)/1.5)
}

func countTokens(msgs []adk.Message, tools []*schema.ToolInfo) int64 {
	var tokens int64
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		tokens += estimateTokens(msg.Content)
		// Multimodal parts (e.g. view_image tool results): text parts are
		// estimated as text; image parts use a flat per-image estimate since
		// provider billing is dimension-based, not base64-length-based.
		for _, part := range msg.UserInputMultiContent {
			switch part.Type {
			case schema.ChatMessagePartTypeText:
				tokens += estimateTokens(part.Text)
			case schema.ChatMessagePartTypeImageURL:
				tokens += estimatedTokensPerImagePart
			}
		}
		if msg.Role == schema.Assistant {
			for _, tc := range msg.ToolCalls {
				tokens += estimateTokens(tc.Function.Name)
				tokens += estimateTokens(tc.Function.Arguments)
			}
		}
	}
	for _, t := range tools {
		text, _ := json.Marshal(t)
		tokens += estimateTokens(string(text))
	}
	return tokens
}

// agentRunTimeout bounds one full ReAct run, not a single model call;
// <= 0 means no timeout.
func agentRunTimeout() time.Duration {
	secs := config.Get().Agent.AgentTimeout
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

func withRunTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if timeout := agentRunTimeout(); timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return ctx, func() {}
}

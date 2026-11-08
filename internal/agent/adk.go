package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
)

// estimateTokens provides a language-aware token estimate.
// ASCII text ≈ 4 chars/token, CJK text ≈ 1.5 chars/token.
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

// countTokens estimates the total tokens of messages and tool definitions.
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

// agentRunTimeout returns the per-attempt timeout for one full agent run
// (the entire ReAct loop of up to max_step tool rounds, not a single Generate
// call). It reads the live config so agent_timeout is hot-reloadable.
// A configured value <= 0 (the default) means no timeout: the run is bounded
// only by the caller's context (user stop) and max_step.
func agentRunTimeout() time.Duration {
	secs := config.Get().Agent.AgentTimeout
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// withRunTimeout wraps ctx with the configured per-run timeout, or returns ctx
// unchanged when agent_timeout is disabled (<= 0, the default).
func withRunTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if timeout := agentRunTimeout(); timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return ctx, func() {}
}

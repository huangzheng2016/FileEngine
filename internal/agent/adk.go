package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

func buildADKAgent(ctx context.Context, model einomodel.ChatModel, tools []tool.BaseTool, instruction string, maxIterations int, contextWindow int) (*adk.ChatModelAgent, error) {
	// 1. patchtoolcalls: 修补悬空 ToolCall
	patchMw, err := patchtoolcalls.New(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create patchtoolcalls middleware: %w", err)
	}

	// 2. reduction: 工具结果截断与清理
	// Dynamic thresholds based on model's context window
	if contextWindow <= 0 {
		contextWindow = db.DefaultContextWindow
	}
	clearThreshold := int64(float64(contextWindow) * 0.75)

	reduceCfg := &reduction.Config{
		SkipTruncation: true,
		TokenCounter: func(_ context.Context, msgs []adk.Message, tools []*schema.ToolInfo) (int64, error) {
			return countTokens(msgs, tools), nil
		},
		MaxTokensForClear:         clearThreshold,
		ClearRetentionSuffixLimit: 2,
	}
	reduceMw, err := reduction.New(ctx, reduceCfg)
	if err != nil {
		return nil, fmt.Errorf("create reduction middleware: %w", err)
	}

	// 3. summarization: 上下文压缩（eino 官方摘要中间件）
	// 触发阈值取模型上下文窗口的 90%，与 reduction 共用同一 token 估算。
	compactTrigger := int(float64(contextWindow) * 0.90)
	if compactTrigger < 1 {
		compactTrigger = 1
	}
	summaryMw, err := summarization.New(ctx, &summarization.Config{
		Model: model,
		TokenCounter: func(_ context.Context, input *summarization.TokenCounterInput) (int, error) {
			return int(countTokens(input.Messages, input.Tools)), nil
		},
		Trigger: &summarization.TriggerCondition{ContextTokens: compactTrigger},
	})
	if err != nil {
		return nil, fmt.Errorf("create summarization middleware: %w", err)
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "fileengine-agent",
		Instruction: instruction,
		Model:       model,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
		},
		MaxIterations: maxIterations,
		Handlers: []adk.ChatModelAgentMiddleware{
			patchMw,
			reduceMw,
			summaryMw,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create chat model agent: %w", err)
	}
	return agent, nil
}

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
func agentRunTimeout() time.Duration {
	secs := config.Get().Agent.AgentTimeout
	if secs <= 0 {
		secs = 60
	}
	return time.Duration(secs) * time.Second
}

func runADKWithRetry(ctx context.Context, agent *adk.ChatModelAgent, messages []*schema.Message, logger *Logger, label string, tracker *tokenTrackingModel) (*schema.Message, error) {
	timeout := agentRunTimeout()
	callCtx, callCancel := context.WithTimeout(ctx, timeout)
	defer callCancel()

	tracker.ResetUsage()
	result, err := runADKOnce(callCtx, agent, messages, logger)
	if err == nil {
		return result, nil
	}

	maxRetries := config.Get().Agent.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	for retry := 1; retry <= maxRetries; retry++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Printf("%s retry %d/%d: %v", label, retry, maxRetries, err)
		time.Sleep(time.Duration(retry) * 2 * time.Second)
		retryCtx, retryCancel := context.WithTimeout(ctx, timeout)
		tracker.ResetUsage()
		result, err = runADKOnce(retryCtx, agent, messages, logger)
		retryCancel()
		if err == nil {
			return result, nil
		}
	}
	logger.LogMessage("system", fmt.Sprintf("Error after %d retries: %v", maxRetries, err), 0, 0, 0)
	return nil, err
}

// runADKOnce executes one full agent run and forwards every ADK AgentEvent to
// the session event bus as step-level protocol events:
//   - assistant message with tool calls -> transient `message` (reasoning
//     text, if any) + one `tool_call` per invocation
//   - tool result message               -> `tool_result`
//   - the final assistant message is NOT emitted here: the caller logs it via
//     LogMessage with token usage (persisted + published there)
func runADKOnce(ctx context.Context, agent *adk.ChatModelAgent, messages []*schema.Message, logger *Logger) (*schema.Message, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iterator := runner.Run(ctx, messages)

	var lastAssistantMsg *schema.Message
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			// Drain remaining events to prevent goroutine/resource leaks
			for {
				_, ok := iterator.Next()
				if !ok {
					break
				}
			}
			return nil, event.Err
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		mo := event.Output.MessageOutput
		msg := mo.Message
		if msg == nil {
			continue
		}
		switch mo.Role {
		case schema.Assistant:
			lastAssistantMsg = msg
			if len(msg.ToolCalls) > 0 {
				if strings.TrimSpace(msg.Content) != "" {
					logger.EmitMessage("assistant", msg.Content)
				}
				for _, tc := range msg.ToolCalls {
					logger.EmitToolCall(tc.Function.Name, tc.Function.Arguments)
				}
			}
		case schema.Tool:
			logger.EmitToolResult(mo.ToolName, msg.Content, true)
		}
	}
	return lastAssistantMsg, nil
}

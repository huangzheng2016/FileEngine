package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/config"

	"github.com/huangzheng2016/KnowCompact/compact"
	einocompact "github.com/huangzheng2016/KnowCompact/eino"
)

func buildADKAgent(ctx context.Context, model einomodel.ChatModel, tools []tool.BaseTool, instruction string, maxIterations int, modelMaxTokens int) (*adk.ChatModelAgent, error) {
	// 1. patchtoolcalls: 修补悬空 ToolCall
	patchMw, err := patchtoolcalls.New(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create patchtoolcalls middleware: %w", err)
	}

	// 2. reduction: 工具结果截断与清理
	// Dynamic thresholds based on model's context window
	if modelMaxTokens <= 0 {
		modelMaxTokens = 128_000
	}
	clearThreshold := int64(float64(modelMaxTokens) * 0.75)
	compactThreshold := int(float64(modelMaxTokens) * 0.90)

	reduceCfg := &reduction.Config{
		SkipTruncation: true,
		TokenCounter: func(_ context.Context, msgs []adk.Message, tools []*schema.ToolInfo) (int64, error) {
			var tokens int64
			for _, msg := range msgs {
				if msg == nil {
					continue
				}
				// Improved token estimation: ASCII ≈ 4 chars/token, CJK ≈ 1.5 chars/token
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
			return tokens, nil
		},
		MaxTokensForClear:         clearThreshold,
		ClearRetentionSuffixLimit: 2,
	}
	reduceMw, err := reduction.New(ctx, reduceCfg)
	if err != nil {
		return nil, fmt.Errorf("create reduction middleware: %w", err)
	}

	// 3. compact: 上下文压缩（KnowCompact）
	compactCfg := compact.DefaultCompactionConfig()
	compactor := einocompact.NewDefaultCompactorWithEinoModel(compactCfg, model, "agent-model")
	compactMw := einocompact.NewCompactMiddleware(compactor, compactThreshold)

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
			compactMw,
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

func runADKWithRetry(ctx context.Context, agent *adk.ChatModelAgent, messages []*schema.Message, logger *Logger, label string, tracker *tokenTrackingModel) (*schema.Message, error) {
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	tracker.ResetUsage()
	result, err := runADKOnce(callCtx, agent, messages)
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
		retryCtx, retryCancel := context.WithTimeout(ctx, 60*time.Second)
		tracker.ResetUsage()
		result, err = runADKOnce(retryCtx, agent, messages)
		retryCancel()
		if err == nil {
			return result, nil
		}
	}
	logger.LogMessage("system", fmt.Sprintf("Error after %d retries: %v", maxRetries, err), 0, 0, 0)
	return nil, err
}

func runADKOnce(ctx context.Context, agent *adk.ChatModelAgent, messages []*schema.Message) (*schema.Message, error) {
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
		if event.Output != nil && event.Output.MessageOutput != nil {
			msg := event.Output.MessageOutput.Message
			if msg != nil && msg.Role == schema.Assistant {
				lastAssistantMsg = msg
			}
		}
	}
	return lastAssistantMsg, nil
}

package agent

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"
)

// reductionProfile sets how aggressively old tool results are cleared and
// single results truncated for one kind of run.
type reductionProfile struct {
	clearAt    func(contextWindow int) int64 // token count that triggers clearing
	keepRounds int                           // most recent tool rounds never cleared
	truncChars int                           // per-result cap in characters
}

var (
	// interactiveReduction: survey / review / chat / instruct runs — clear
	// late (75% of the window), truncate only huge single results.
	interactiveReduction = reductionProfile{
		clearAt:    func(w int) int64 { return int64(float64(w) * 0.75) },
		keepRounds: 2,
		truncChars: 32000,
	}
	// workerReduction: long-running tagging workers — every pulled unit is
	// self-contained, so old tool results are cleared early (~35K tokens)
	// and single results are capped at ~8K characters.
	workerReduction = reductionProfile{
		clearAt:    func(w int) int64 { return min(35_000, int64(float64(w)*0.75)) },
		keepRounds: 2,
		truncChars: 8000,
	}
)

type agentOptions struct {
	reduction    reductionProfile
	summaryModel einomodel.ChatModel
}

// agentOption customizes buildADKAgent.
type agentOption func(*agentOptions)

// withWorkerReduction applies the long-running worker reduction profile.
func withWorkerReduction() agentOption {
	return func(o *agentOptions) { o.reduction = workerReduction }
}

// withSummaryModel makes the summarization middleware use m (the session's
// bulk model) instead of the agent's own model.
func withSummaryModel(m einomodel.ChatModel) agentOption {
	return func(o *agentOptions) {
		if m != nil {
			o.summaryModel = m
		}
	}
}

// bulkSummaryOption makes summarization of a main-model run use the session's
// bulk model (when one is set), counted by the run's tracker.
func bulkSummaryOption(ctx context.Context, repo *db.Repository, session *db.ScanSession, main *db.ModelProvider, tracker *tokenTrackingModel) (agentOption, error) {
	bulk, err := modelfactory.ResolveBulkProvider(repo, session, main)
	if err != nil || bulk == main {
		return func(*agentOptions) {}, err
	}
	m, err := modelfactory.NewChatModelFromProvider(ctx, bulk)
	if err != nil {
		return nil, fmt.Errorf("create bulk model: %w", err)
	}
	return withSummaryModel(tracker.sibling(m)), nil
}

// buildADKAgent creates the ReAct agent of one worker/run: per-request
// class-aware retries (ModelRetryConfig) and the middleware chain
// patchtoolcalls → reduction (truncate single results, clear old ones) →
// summarization (90% of the context window).
func buildADKAgent(ctx context.Context, model einomodel.ChatModel, tools []tool.BaseTool, instruction string, maxIterations int, contextWindow int, opts ...agentOption) (*adk.ChatModelAgent, error) {
	o := agentOptions{reduction: interactiveReduction, summaryModel: model}
	for _, opt := range opts {
		opt(&o)
	}
	if contextWindow <= 0 {
		contextWindow = db.DefaultContextWindow
	}

	patchMw, err := patchtoolcalls.New(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create patchtoolcalls middleware: %w", err)
	}

	reduceMw, err := buildReduction(ctx, tools, o.reduction, contextWindow)
	if err != nil {
		return nil, err
	}

	compactTrigger := max(int(float64(contextWindow)*0.90), 1)
	summaryMw, err := summarization.New(ctx, &summarization.Config{
		Model: o.summaryModel,
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
		MaxIterations:    maxIterations,
		ModelRetryConfig: modelRetryConfig(),
		Handlers:         []adk.ChatModelAgentMiddleware{patchMw, reduceMw, summaryMw},
	})
	if err != nil {
		return nil, fmt.Errorf("create chat model agent: %w", err)
	}
	return agent, nil
}

// buildReduction clears old tool results past the profile's threshold
// (keeping the last rounds) and truncates every single tool result to the
// profile's character cap. Nothing is offloaded: a truncated result tells the
// model to narrow its query instead.
func buildReduction(ctx context.Context, tools []tool.BaseTool, p reductionProfile, contextWindow int) (adk.ChatModelAgentMiddleware, error) {
	trunc := &reduction.ToolReductionConfig{TruncHandler: truncateToolResult(p.truncChars)}
	perTool := make(map[string]*reduction.ToolReductionConfig, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("tool info: %w", err)
		}
		perTool[info.Name] = trunc
	}
	mw, err := reduction.New(ctx, &reduction.Config{
		SkipTruncation: true, // the default handler offloads to files; per-tool handlers truncate in place
		TokenCounter: func(_ context.Context, msgs []adk.Message, tools []*schema.ToolInfo) (int64, error) {
			return countTokens(msgs, tools), nil
		},
		MaxTokensForClear:         p.clearAt(contextWindow),
		ClearRetentionSuffixLimit: p.keepRounds,
		ToolConfig:                perTool,
	})
	if err != nil {
		return nil, fmt.Errorf("create reduction middleware: %w", err)
	}
	return mw, nil
}

// truncateToolResult caps every text part of a tool result at maxChars
// characters; image parts pass through.
func truncateToolResult(maxChars int) func(context.Context, *reduction.ToolDetail) (*reduction.TruncResult, error) {
	return func(_ context.Context, d *reduction.ToolDetail) (*reduction.TruncResult, error) {
		if d.ToolResult == nil {
			return &reduction.TruncResult{NeedTrunc: false}, nil
		}
		parts := make([]schema.ToolOutputPart, len(d.ToolResult.Parts))
		cut := false
		for i, part := range d.ToolResult.Parts {
			parts[i] = part
			if part.Type != schema.ToolPartTypeText {
				continue
			}
			if n := utf8.RuneCountInString(part.Text); n > maxChars {
				parts[i].Text = string([]rune(part.Text)[:maxChars]) +
					fmt.Sprintf("\n…[输出过长已截断：共 %d 字符，仅保留前 %d。请缩小范围（limit / offset / 更具体的路径）后重试]", n, maxChars)
				cut = true
			}
		}
		if !cut {
			return &reduction.TruncResult{NeedTrunc: false}, nil
		}
		return &reduction.TruncResult{NeedTrunc: true, ToolResult: &schema.ToolResult{Parts: parts}}, nil
	}
}

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

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"
)

// Clearing rewrites history in place, which ends the cached prefix at the
// first cleared message. It therefore runs in batches: only above the
// threshold, only when one clear frees at least clearAtLeast tokens, and the
// newest keepRounds tool rounds stay intact.
type reductionProfile struct {
	clearAt      func(contextWindow int) int64 // token count that triggers clearing
	clearAtLeast func(clearAt int64) int64     // tokens one clear must free, else none happens
	keepRounds   int                           // most recent tool rounds never cleared
	truncChars   int                           // per-result cap in characters
}

const (
	minClearTokens = 10_000
	// Arguments of cleared calls (decision batches, scripts) longer than this
	// are replaced; shorter ones stay as the record of what was asked.
	clearedArgsMaxChars = 600
)

var (
	interactiveReduction = reductionProfile{
		clearAt:      func(w int) int64 { return int64(float64(w) * 0.75) },
		clearAtLeast: func(at int64) int64 { return max(minClearTokens, at/5) },
		keepRounds:   3,
		truncChars:   32000,
	}
	// Every pulled unit is self-contained, so workers clear old tool results
	// early.
	workerReduction = reductionProfile{
		clearAt:      func(w int) int64 { return min(35_000, int64(float64(w)*0.75)) },
		clearAtLeast: func(int64) int64 { return minClearTokens },
		keepRounds:   3,
		truncChars:   8000,
	}
)

type agentOptions struct {
	reduction    reductionProfile
	summaryModel einomodel.ChatModel
}

type agentOption func(*agentOptions)

func withWorkerReduction() agentOption {
	return func(o *agentOptions) { o.reduction = workerReduction }
}

func withSummaryModel(m einomodel.ChatModel) agentOption {
	return func(o *agentOptions) {
		if m != nil {
			o.summaryModel = m
		}
	}
}

func bulkSummaryOption(ctx context.Context, repo *db.Repository, session *model.ScanSession, main *model.ModelProvider, tracker *tokenTrackingModel) (agentOption, error) {
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

func buildADKAgent(ctx context.Context, chatModel einomodel.ChatModel, tools []tool.BaseTool, instruction string, maxIterations int, contextWindow int, opts ...agentOption) (*adk.ChatModelAgent, error) {
	o := agentOptions{reduction: interactiveReduction, summaryModel: chatModel}
	for _, opt := range opts {
		opt(&o)
	}
	if contextWindow <= 0 {
		contextWindow = model.DefaultContextWindow
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
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools, UnknownToolsHandler: unknownToolHandler},
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

// Nothing is offloaded: a truncated result tells the model to narrow its
// query instead.
func buildReduction(ctx context.Context, tools []tool.BaseTool, p reductionProfile, contextWindow int) (adk.ChatModelAgentMiddleware, error) {
	trunc := &reduction.ToolReductionConfig{TruncHandler: truncateToolResult(p.truncChars), ClearHandler: clearToolResult}
	perTool := make(map[string]*reduction.ToolReductionConfig, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("tool info: %w", err)
		}
		perTool[info.Name] = trunc
	}
	clearAt := p.clearAt(contextWindow)
	mw, err := reduction.New(ctx, &reduction.Config{
		SkipTruncation: true, // the default handler offloads to files; per-tool handlers truncate in place
		TokenCounter: func(_ context.Context, msgs []adk.Message, tools []*schema.ToolInfo) (int64, error) {
			return countTokens(msgs, tools), nil
		},
		MaxTokensForClear:         clearAt,
		ClearAtLeastTokens:        p.clearAtLeast(clearAt),
		ClearRetentionSuffixLimit: p.keepRounds,
		ToolConfig:                perTool,
	})
	if err != nil {
		return nil, fmt.Errorf("create reduction middleware: %w", err)
	}
	return mw, nil
}

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

// clearToolResult replaces the whole result, image parts included, with one
// short text, and drops the bulky arguments of the call.
func clearToolResult(_ context.Context, d *reduction.ToolDetail) (*reduction.ClearResult, error) {
	res := &reduction.ClearResult{
		NeedClear:    true,
		ToolArgument: d.ToolArgument,
		ToolResult: &schema.ToolResult{Parts: []schema.ToolOutputPart{
			{Type: schema.ToolPartTypeText, Text: "[较早的工具结果已清除；需要时请重新查询]"},
		}},
	}
	if d.ToolArgument != nil && utf8.RuneCountInString(d.ToolArgument.Text) > clearedArgsMaxChars {
		res.ToolArgument = &schema.ToolArgument{Text: `{"已清除":"较早的调用参数过长，已省略"}`}
	}
	return res, nil
}

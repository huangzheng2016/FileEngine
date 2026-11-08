package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

// RequestType identifies the kind of operation requested.
type RequestType string

const (
	RequestAutoTag  RequestType = "autotag"
	RequestInstruct RequestType = "instruct"
)

// OrchestratorRequest is the unified request for the orchestrator.
type OrchestratorRequest struct {
	Type       RequestType
	UserPrompt string
	Files      []db.FileEntry
}

// OrchestratorAgent routes requests to specialized sub-agents and coordinates execution.
type OrchestratorAgent struct {
	repo          *db.Repository
	fsCfg         config.RemoteFSConfig
	modelProvider *db.ModelProvider
	session       *db.ScanSession
	sessionID     uint
	logger        *Logger
	partial       bool // the last auto-tag run had failed batches
}

// Partial reports whether the last auto-tag run had failed batches.
func (o *OrchestratorAgent) Partial() bool { return o.partial }

func NewOrchestratorAgent(repo *db.Repository, fsCfg config.RemoteFSConfig, modelProvider *db.ModelProvider, session *db.ScanSession, sessionID uint, logger *Logger) *OrchestratorAgent {
	return &OrchestratorAgent{
		repo:          repo,
		fsCfg:         fsCfg,
		modelProvider: modelProvider,
		session:       session,
		sessionID:     sessionID,
		logger:        logger,
	}
}

// DetectRequestType analyzes a user prompt to determine the best agent to handle it.
func DetectRequestType(prompt string) RequestType {
	lower := strings.ToLower(prompt)
	// Keywords suggesting classification/organization
	autoKeywords := []string{"整理", "分类", "归档", "自动", "organize", "classify", "sort", "auto"}
	for _, kw := range autoKeywords {
		if strings.Contains(lower, kw) {
			return RequestAutoTag
		}
	}
	return RequestInstruct
}

// Run executes the request by routing to the appropriate sub-agent and summarizing results.
func (o *OrchestratorAgent) Run(ctx context.Context, req OrchestratorRequest) (string, error) {
	switch req.Type {
	case RequestAutoTag:
		return o.runAutoTag(ctx)
	case RequestInstruct:
		return o.runInstruct(ctx, req.Files, req.UserPrompt)
	default:
		// Fallback: try to detect from prompt
		if req.UserPrompt != "" {
			detected := DetectRequestType(req.UserPrompt)
			if detected == RequestAutoTag {
				return o.runAutoTag(ctx)
			}
		}
		return o.runInstruct(ctx, req.Files, req.UserPrompt)
	}
}

func (o *OrchestratorAgent) runAutoTag(ctx context.Context) (string, error) {
	log.Printf("orchestrator: routing to AutoTagAgent for session %d", o.sessionID)

	autoTag := NewAutoTagAgent(o.repo, o.fsCfg, o.modelProvider, o.session, o.sessionID, o.logger)
	if err := autoTag.Run(ctx); err != nil {
		return "", fmt.Errorf("auto-tag failed: %w", err)
	}

	// Surface partial failure: failed batches (retries exhausted) make the
	// run partial and get their own report section.
	failures := autoTag.Failures()
	o.partial = len(failures) > 0

	// "建了不用" discipline, enforced at run end (never mid-run — a category
	// created by an earlier batch may still be filled by a later one):
	// AI-created categories with zero planned files are pruned and recorded.
	pruned := o.pruneEmptyAgentCategories()

	// Generate summary
	summary := NewSummaryAgent(o.repo, o.sessionID)
	report, err := summary.Run(ctx)
	if err != nil {
		log.Printf("orchestrator: summary generation failed: %v", err)
		report = "自动分类完成"
	}
	report += failureReportSection(failures)
	report += emptyCategoryPruneSection(pruned)
	// Reconciliation: split the untagged remainder by entry type so the
	// expected deltas (container dirs, symlinks) are distinguishable from
	// real tagging misses (files); junk-filter ignores are reported too.
	var ignored int64
	if n, err := o.repo.CountIgnoredJunk(o.sessionID); err == nil {
		ignored = n
	}
	if breakdown, err := o.repo.CountUntaggedByType(o.sessionID); err != nil {
		log.Printf("orchestrator: untagged reconciliation failed: %v", err)
	} else {
		report += untaggedReconciliationSection(breakdown, ignored)
	}
	return report, nil
}

// pruneEmptyAgentCategories deletes AI-created categories that ended the run
// with zero planned files and returns their "name (path)" labels for the
// report. User-created categories (agent_created=false) and non-empty ones
// are never touched. Runs at run end only: categories created mid-run may
// still be filled by later batches.
//
// Emptiness is judged FILESYSTEM-WIDE (all sessions), not per-session:
// categories are filesystem-level shared resources — in a rescan workflow
// (new session + exclude_category_dirs) a category created and filled by an
// earlier run has zero plans in the current session, and pruning it would
// destroy still-referenced organization metadata.
func (o *OrchestratorAgent) pruneEmptyAgentCategories() []string {
	cats, err := o.repo.ListCategories(o.session.FilesystemID)
	if err != nil {
		log.Printf("orchestrator: empty-category prune, list failed: %v", err)
		return nil
	}
	var pruned []string
	for _, c := range cats {
		if !c.AgentCreated {
			continue
		}
		n, err := o.repo.CountPlannedByTargetPath(o.session.FilesystemID, c.Path)
		if err != nil {
			log.Printf("orchestrator: empty-category prune, count for %q failed: %v", c.Name, err)
			continue
		}
		if n > 0 {
			continue
		}
		if err := o.repo.DeleteCategory(c.ID); err != nil {
			log.Printf("orchestrator: delete empty agent category %q failed: %v", c.Name, err)
			continue
		}
		pruned = append(pruned, fmt.Sprintf("%s (%s)", c.Name, c.Path))
	}
	return pruned
}

func (o *OrchestratorAgent) runInstruct(ctx context.Context, files []db.FileEntry, userPrompt string) (string, error) {
	log.Printf("orchestrator: routing to InstructAgent for session %d", o.sessionID)

	instruct := NewInstructAgent(o.repo, o.fsCfg, o.modelProvider, o.session, o.sessionID, o.logger)
	response, err := instruct.Run(ctx, files, userPrompt)
	if err != nil {
		return "", fmt.Errorf("instruct failed: %w", err)
	}

	// Generate summary
	summary := NewSummaryAgent(o.repo, o.sessionID)
	report, err := summary.Run(ctx)
	if err != nil {
		log.Printf("orchestrator: summary generation failed: %v", err)
		return response, nil
	}

	return response + "\n\n---\n\n" + report, nil
}

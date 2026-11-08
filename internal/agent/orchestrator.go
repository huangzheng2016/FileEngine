package agent

import (
	"context"
	"fmt"
	"log"

	"FileEngine/internal/config"
	"FileEngine/internal/db"
)

type RequestType string

const RequestAutoTag RequestType = "autotag"

type OrchestratorRequest struct {
	Type RequestType
}

type OrchestratorAgent struct {
	repo          *db.Repository
	fsCfg         config.RemoteFSConfig
	modelProvider *db.ModelProvider
	session       *db.ScanSession
	sessionID     uint
	logger        *Logger
	partial       bool
}

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

func (o *OrchestratorAgent) Run(ctx context.Context, req OrchestratorRequest) (string, error) {
	switch req.Type {
	case RequestAutoTag:
		return o.runAutoTag(ctx)
	default:
		return "", fmt.Errorf("unknown request type %q", req.Type)
	}
}

func (o *OrchestratorAgent) runAutoTag(ctx context.Context) (string, error) {
	log.Printf("orchestrator: routing to AutoTagAgent for session %d", o.sessionID)

	autoTag := NewAutoTagAgent(o.repo, o.fsCfg, o.modelProvider, o.session, o.sessionID, o.logger)
	if err := autoTag.Run(ctx); err != nil {
		return "", fmt.Errorf("auto-tag failed: %w", err)
	}

	failures := autoTag.Failures()
	o.partial = len(failures) > 0

	// Pruned at run end only: a category created by an earlier unit may still be
	// filled by a later one.
	pruned := o.pruneEmptyAgentCategories()

	summary := NewSummaryAgent(o.repo, o.sessionID)
	report, err := summary.Run(ctx)
	if err != nil {
		log.Printf("orchestrator: summary generation failed: %v", err)
		report = "自动分类完成"
	}
	report += failureReportSection(failures)
	report += emptyCategoryPruneSection(pruned)
	// Container dirs and symlinks are expected remainders; only files are real
	// misses.
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

// Emptiness is judged across all sessions of the filesystem: in a rescan, a
// category filled by an earlier session has no plans in the current one.
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

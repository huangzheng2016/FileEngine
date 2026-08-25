package agent

import (
	"context"
	"errors"
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
}

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

	// Update session status
	session, err := o.repo.GetSession(o.sessionID)
	if err != nil {
		return "", err
	}
	session.Status = "tagging"
	if err := o.repo.UpdateSession(session); err != nil {
		return "", err
	}
	o.emitRunStatus(db.PhaseTagging, db.StatusStarted, "")

	autoTag := NewAutoTagAgent(o.repo, o.fsCfg, o.modelProvider, o.session, o.sessionID)
	if err := autoTag.Run(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			o.emitRunStatus(db.PhaseTagging, db.StatusStopped, "")
		} else {
			o.emitRunStatus(db.PhaseTagging, db.StatusError, err.Error())
		}
		return "", fmt.Errorf("auto-tag failed: %w", err)
	}

	// Surface partial failure: failed batches (retries exhausted) are reflected
	// in the session status, the run_status message, and the final report.
	failures := autoTag.Failures()

	// Update session status
	session, err = o.repo.GetSession(o.sessionID)
	if err != nil {
		return "", err
	}
	session.Status = "tagged"
	if len(failures) > 0 {
		session.Status = "tagged_partial"
	}
	if err := o.repo.UpdateSession(session); err != nil {
		return "", err
	}
	doneMsg := ""
	if len(failures) > 0 {
		doneMsg = fmt.Sprintf("%d batch(es) failed after retries", len(failures))
	}
	o.emitRunStatus(db.PhaseTagging, db.StatusDone, doneMsg)

	// Generate summary
	summary := NewSummaryAgent(o.repo, o.sessionID)
	report, err := summary.Run(ctx)
	if err != nil {
		log.Printf("orchestrator: summary generation failed: %v", err)
		report = "自动分类完成"
	}
	report += failureReportSection(failures)
	return report, nil
}

func (o *OrchestratorAgent) runInstruct(ctx context.Context, files []db.FileEntry, userPrompt string) (string, error) {
	log.Printf("orchestrator: routing to InstructAgent for session %d", o.sessionID)

	o.emitRunStatus(db.PhaseInstruct, db.StatusStarted, "")
	instruct := NewInstructAgent(o.repo, o.fsCfg, o.modelProvider, o.session, o.sessionID, o.logger)
	response, err := instruct.Run(ctx, files, userPrompt)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			o.emitRunStatus(db.PhaseInstruct, db.StatusStopped, "")
		} else {
			o.emitRunStatus(db.PhaseInstruct, db.StatusError, err.Error())
		}
		return "", fmt.Errorf("instruct failed: %w", err)
	}
	o.emitRunStatus(db.PhaseInstruct, db.StatusDone, "")

	// Generate summary
	summary := NewSummaryAgent(o.repo, o.sessionID)
	report, err := summary.Run(ctx)
	if err != nil {
		log.Printf("orchestrator: summary generation failed: %v", err)
		return response, nil
	}

	return response + "\n\n---\n\n" + report, nil
}

// emitRunStatus publishes a run_status event mirroring a phase transition.
func (o *OrchestratorAgent) emitRunStatus(phase, status, message string) {
	db.PublishEvent(db.Event{Type: db.EventRunStatus, SessionID: o.sessionID, Payload: db.RunStatusPayload{
		SessionID: o.sessionID,
		Phase:     phase,
		Status:    status,
		Message:   message,
	}})
}

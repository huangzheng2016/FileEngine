package agent

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"FileEngine/internal/db"
)

const logBufferSize = 20

type Logger struct {
	repo      *db.Repository
	sessionID uint
	batch     int
	buffer    []*db.AgentLog
	mu        sync.Mutex
}

func NewLogger(repo *db.Repository, sessionID uint) *Logger {
	return &Logger{
		repo:      repo,
		sessionID: sessionID,
		buffer:    make([]*db.AgentLog, 0, logBufferSize),
	}
}

func (l *Logger) SetBatch(batch int) {
	l.mu.Lock()
	l.batch = batch
	l.mu.Unlock()
}

func (l *Logger) GetBatch() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.batch
}

func (l *Logger) LogMessage(role, content string, promptTokens, completionTokens, totalTokens int) {
	l.mu.Lock()
	batch := l.batch
	l.mu.Unlock()

	log := &db.AgentLog{
		ScanSessionID:    l.sessionID,
		BatchIndex:       batch,
		Role:             role,
		Content:          content,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		CreatedAt:        time.Now(),
	}
	l.save(log)
	db.PublishEvent(db.Event{Type: db.EventMessage, SessionID: l.sessionID, Payload: db.MessagePayload{
		SessionID:        l.sessionID,
		BatchIndex:       batch,
		Role:             role,
		Content:          content,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	}})
}

// EmitMessage publishes a transient message event without persisting it.
// Used for intermediate assistant steps; the final assistant message of a run
// goes through LogMessage (persisted + published, with token counts).
func (l *Logger) EmitMessage(role, content string) {
	db.PublishEvent(db.Event{Type: db.EventMessage, SessionID: l.sessionID, Payload: db.MessagePayload{
		SessionID:  l.sessionID,
		BatchIndex: l.GetBatch(),
		Role:       role,
		Content:    content,
	}})
}

// EmitBatchStart announces the logger's current batch on the session bus.
func (l *Logger) EmitBatchStart(label string) {
	db.PublishEvent(db.Event{Type: db.EventBatchStart, SessionID: l.sessionID, Payload: db.BatchStartPayload{
		SessionID:  l.sessionID,
		BatchIndex: l.GetBatch(),
		Label:      label,
	}})
}

// EmitToolCall publishes one model-requested tool invocation.
func (l *Logger) EmitToolCall(toolName, input string) {
	db.PublishEvent(db.Event{Type: db.EventToolCall, SessionID: l.sessionID, Payload: db.ToolCallPayload{
		SessionID:  l.sessionID,
		BatchIndex: l.GetBatch(),
		ToolName:   toolName,
		Input:      input,
	}})
}

// EmitToolResult publishes one tool execution result.
func (l *Logger) EmitToolResult(toolName, output string, ok bool) {
	db.PublishEvent(db.Event{Type: db.EventToolResult, SessionID: l.sessionID, Payload: db.ToolResultPayload{
		SessionID:  l.sessionID,
		BatchIndex: l.GetBatch(),
		ToolName:   toolName,
		Output:     output,
		OK:         ok,
	}})
}

func (l *Logger) LogToolCall(toolName string, input, output interface{}) {
	l.mu.Lock()
	batch := l.batch
	l.mu.Unlock()

	inputJSON, _ := json.Marshal(input)
	outputJSON, _ := json.Marshal(output)

	log := &db.AgentLog{
		ScanSessionID: l.sessionID,
		BatchIndex:    batch,
		Role:          "tool",
		ToolName:      toolName,
		ToolInput:     string(inputJSON),
		ToolOutput:    string(outputJSON),
		CreatedAt:     time.Now(),
	}
	l.save(log)
}

func (l *Logger) save(entry *db.AgentLog) {
	l.mu.Lock()
	l.buffer = append(l.buffer, entry)
	shouldFlush := len(l.buffer) >= logBufferSize
	l.mu.Unlock()

	if shouldFlush {
		l.Flush()
	}
}

// Flush writes all buffered logs to DB in a single batch.
func (l *Logger) Flush() {
	l.mu.Lock()
	if len(l.buffer) == 0 {
		l.mu.Unlock()
		return
	}
	batch := l.buffer
	l.buffer = make([]*db.AgentLog, 0, logBufferSize)
	l.mu.Unlock()

	if err := db.WithRetry(func() error {
		return l.repo.CreateAgentLogs(batch)
	}); err != nil {
		log.Printf("logger: failed to flush %d logs: %v", len(batch), err)
	}
}

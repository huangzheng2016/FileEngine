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
	workerID  int
	batch     int
	buffer    []*db.AgentLog
	mu        sync.Mutex
}

// NewLogger creates a session-scoped logger for one agent worker lane.
// workerID lands in every SSE payload and persisted row so the frontend can
// render per-worker lanes; single-worker runs (instruct) pass 0.
func NewLogger(repo *db.Repository, sessionID uint, workerID int) *Logger {
	return &Logger{
		repo:      repo,
		sessionID: sessionID,
		workerID:  workerID,
		buffer:    make([]*db.AgentLog, 0, logBufferSize),
	}
}

func (l *Logger) WorkerID() int {
	return l.workerID
}

// SetBatch moves the internal batch cursor (persisted as agent_logs.batch_index
// and stamped onto file_entries by mark_tagged). It is no longer exposed on
// the event stream — lanes replaced batch grouping there.
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
		WorkerID:         l.workerID,
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
		WorkerID:         l.workerID,
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
		SessionID: l.sessionID,
		WorkerID:  l.workerID,
		Role:      role,
		Content:   content,
	}})
}

// EmitPullBegin announces this worker starting one work unit (directory,
// loose-file pull, instruct run) on the session bus. count is the number of
// files pulled (0 for directory/instruct units); cursor is the starting
// cursor (smallest file id of a loose-file pull).
func (l *Logger) EmitPullBegin(label string, count int, cursor uint) {
	db.PublishEvent(db.Event{Type: db.EventPullBegin, SessionID: l.sessionID, Payload: db.PullBeginPayload{
		SessionID: l.sessionID,
		WorkerID:  l.workerID,
		Label:     label,
		Count:     count,
		Cursor:    cursor,
	}})
}

// EmitPullDone settles this worker's current work unit: tagged files
// classified, failed files missed or failed outright.
func (l *Logger) EmitPullDone(label string, tagged, failed int) {
	db.PublishEvent(db.Event{Type: db.EventPullDone, SessionID: l.sessionID, Payload: db.PullDonePayload{
		SessionID: l.sessionID,
		WorkerID:  l.workerID,
		Label:     label,
		Tagged:    tagged,
		Failed:    failed,
	}})
}

// EmitToolCall publishes one model-requested tool invocation.
func (l *Logger) EmitToolCall(toolName, input string) {
	db.PublishEvent(db.Event{Type: db.EventToolCall, SessionID: l.sessionID, Payload: db.ToolCallPayload{
		SessionID: l.sessionID,
		WorkerID:  l.workerID,
		ToolName:  toolName,
		Input:     input,
	}})
}

// EmitToolResult publishes one tool execution result.
func (l *Logger) EmitToolResult(toolName, output string, ok bool) {
	db.PublishEvent(db.Event{Type: db.EventToolResult, SessionID: l.sessionID, Payload: db.ToolResultPayload{
		SessionID: l.sessionID,
		WorkerID:  l.workerID,
		ToolName:  toolName,
		Output:    output,
		OK:        ok,
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
		WorkerID:      l.workerID,
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

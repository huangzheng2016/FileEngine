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
	listeners []chan *db.AgentLog
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

func (l *Logger) Subscribe() chan *db.AgentLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	ch := make(chan *db.AgentLog, 100)
	l.listeners = append(l.listeners, ch)
	return ch
}

func (l *Logger) Unsubscribe(ch chan *db.AgentLog) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, listener := range l.listeners {
		if listener == ch {
			l.listeners = append(l.listeners[:i], l.listeners[i+1:]...)
			close(ch)
			return
		}
	}
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
	// Copy listeners for broadcast
	listeners := make([]chan *db.AgentLog, len(l.listeners))
	copy(listeners, l.listeners)
	l.mu.Unlock()

	// Broadcast to WebSocket listeners immediately (in-memory, no contention)
	for _, ch := range listeners {
		select {
		case ch <- entry:
		default:
		}
	}

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

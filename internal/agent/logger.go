package agent

import (
	"encoding/json"
	"sync"
	"time"

	"FileEngine/internal/db"
)

type Logger struct {
	repo      *db.Repository
	sessionID uint
	batch     int
	mu        sync.Mutex
	listeners []chan *db.AgentLog
}

func NewLogger(repo *db.Repository, sessionID uint) *Logger {
	return &Logger{
		repo:      repo,
		sessionID: sessionID,
	}
}

func (l *Logger) SetBatch(batch int) {
	l.mu.Lock()
	l.batch = batch
	l.mu.Unlock()
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

func (l *Logger) LogMessage(role, content string, tokensUsed int) {
	l.mu.Lock()
	batch := l.batch
	l.mu.Unlock()

	log := &db.AgentLog{
		ScanSessionID: l.sessionID,
		BatchIndex:    batch,
		Role:          role,
		Content:       content,
		TokensUsed:    tokensUsed,
		CreatedAt:     time.Now(),
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

func (l *Logger) save(log *db.AgentLog) {
	_ = l.repo.CreateAgentLog(log)

	l.mu.Lock()
	listeners := make([]chan *db.AgentLog, len(l.listeners))
	copy(listeners, l.listeners)
	l.mu.Unlock()

	for _, ch := range listeners {
		select {
		case ch <- log:
		default:
		}
	}
}

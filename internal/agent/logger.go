package agent

import (
	"fmt"
	"sync"
	"sync/atomic"

	"FileEngine/internal/db"
)

type runUsage struct {
	mu     sync.Mutex
	tokens db.TokenCounts
	seq    atomic.Uint64
}

type Logger struct {
	sessionID uint
	runID     uint
	workerID  int
	usage     *runUsage

	mu     sync.Mutex
	batch  int
	unitID string
}

func NewLogger(sessionID, runID uint, workerID int) *Logger {
	return &Logger{sessionID: sessionID, runID: runID, workerID: workerID, usage: &runUsage{}}
}

func (l *Logger) ForWorker(workerID int) *Logger {
	return &Logger{sessionID: l.sessionID, runID: l.runID, workerID: workerID, usage: l.usage}
}

func (l *Logger) SessionID() uint { return l.sessionID }
func (l *Logger) RunID() uint     { return l.runID }
func (l *Logger) WorkerID() int   { return l.workerID }

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

func (l *Logger) nextID(prefix string) string {
	return fmt.Sprintf("%s%d-%d", prefix, l.workerID, l.usage.seq.Add(1))
}

func (l *Logger) NewMsgID() string { return l.nextID("m") }

func (l *Logger) AddUsage(t db.TokenCounts) {
	l.usage.mu.Lock()
	defer l.usage.mu.Unlock()
	u := &l.usage.tokens
	u.Prompt += t.Prompt
	u.Cached += t.Cached
	u.CacheWrite += t.CacheWrite
	u.Completion += t.Completion
	u.Total += t.Total
}

func (l *Logger) Tokens() db.TokenCounts {
	l.usage.mu.Lock()
	defer l.usage.mu.Unlock()
	return l.usage.tokens
}

func (l *Logger) emit(typ string, data any) {
	db.Emit(l.sessionID, l.runID, typ, data)
}

func (l *Logger) LogMessage(role, content string) {
	l.EmitMessage(l.NewMsgID(), role, content)
}

func (l *Logger) EmitMessage(msgID, role, content string) {
	l.emit(db.EvMessage, db.MessageData{RunID: l.runID, WorkerID: l.workerID, MsgID: msgID, Role: role, Content: content})
}

func (l *Logger) EmitDelta(typ, msgID, delta string) {
	if delta == "" {
		return
	}
	l.emit(typ, db.DeltaData{RunID: l.runID, WorkerID: l.workerID, MsgID: msgID, Delta: delta})
}

func (l *Logger) EmitToolCall(callID, name string, args any) {
	l.emit(db.EvToolCall, db.ToolCallData{RunID: l.runID, WorkerID: l.workerID, CallID: callID, Name: name, Args: args})
}

func (l *Logger) EmitToolResult(r db.ToolResultData) {
	r.RunID, r.WorkerID = l.runID, l.workerID
	l.emit(db.EvToolResult, r)
}

func (l *Logger) EmitPullBegin(label string, count int) {
	id := l.nextID("u")
	l.mu.Lock()
	l.unitID = id
	l.mu.Unlock()
	l.emit(db.EvPullBegin, db.PullBeginData{RunID: l.runID, WorkerID: l.workerID, UnitID: id, Label: label, Count: count})
}

func (l *Logger) EmitPullDone(tagged, failed int) {
	l.mu.Lock()
	id := l.unitID
	l.unitID = ""
	l.mu.Unlock()
	if id == "" {
		return
	}
	l.emit(db.EvPullDone, db.PullDoneData{RunID: l.runID, WorkerID: l.workerID, UnitID: id, Tagged: tagged, Failed: failed})
}

func (l *Logger) EmitProgress(p db.ProgressData) {
	p.RunID = l.runID
	l.emit(db.EvProgress, p)
}

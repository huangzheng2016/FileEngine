package agent

import (
	"fmt"
	"sync"
	"sync/atomic"

	"FileEngine/internal/db"
)

// runUsage is the state shared by all loggers of one run: token usage and
// the id counter that keeps msg / call / unit ids unique within the run.
type runUsage struct {
	mu     sync.Mutex
	tokens db.TokenCounts
	seq    atomic.Uint64
}

// Logger emits the stream events of one worker lane of one run. Loggers of
// the same run (ForWorker) share the run's token accumulator, which feeds
// AgentRun's token columns when the run finishes.
type Logger struct {
	sessionID uint
	runID     uint
	workerID  int
	usage     *runUsage

	mu     sync.Mutex
	batch  int
	unitID string
}

// NewLogger creates the logger of worker lane workerID of a run.
func NewLogger(sessionID, runID uint, workerID int) *Logger {
	return &Logger{sessionID: sessionID, runID: runID, workerID: workerID, usage: &runUsage{}}
}

// ForWorker returns a logger for another worker lane of the same run.
func (l *Logger) ForWorker(workerID int) *Logger {
	return &Logger{sessionID: l.sessionID, runID: l.runID, workerID: workerID, usage: l.usage}
}

func (l *Logger) SessionID() uint { return l.sessionID }
func (l *Logger) RunID() uint     { return l.runID }
func (l *Logger) WorkerID() int   { return l.workerID }

// SetBatch moves the internal batch cursor stamped onto file_entries by
// mark_tagged. It never appears on the event stream.
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

// nextID returns a run-unique id with the given prefix ("m", "call", "u").
func (l *Logger) nextID(prefix string) string {
	return fmt.Sprintf("%s%d-%d", prefix, l.workerID, l.usage.seq.Add(1))
}

// NewMsgID allocates the msg_id of a new message on this lane.
func (l *Logger) NewMsgID() string { return l.nextID("m") }

// AddUsage adds one attempt's token usage to the run.
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

// Tokens returns the run's accumulated token usage.
func (l *Logger) Tokens() db.TokenCounts {
	l.usage.mu.Lock()
	defer l.usage.mu.Unlock()
	return l.usage.tokens
}

func (l *Logger) emit(typ string, data any) {
	db.Emit(l.sessionID, l.runID, typ, data)
}

// LogMessage emits one complete message (role user / assistant / system).
func (l *Logger) LogMessage(role, content string) {
	l.EmitMessage(l.NewMsgID(), role, content)
}

// EmitMessage emits a complete message with a known msg_id; for assistant
// messages it finalizes the text deltas streamed under the same msg_id.
func (l *Logger) EmitMessage(msgID, role, content string) {
	l.emit(db.EvMessage, db.MessageData{RunID: l.runID, WorkerID: l.workerID, MsgID: msgID, Role: role, Content: content})
}

// EmitDelta emits a text or reasoning delta of an assistant message.
func (l *Logger) EmitDelta(typ, msgID, delta string) {
	if delta == "" {
		return
	}
	l.emit(typ, db.DeltaData{RunID: l.runID, WorkerID: l.workerID, MsgID: msgID, Delta: delta})
}

// EmitToolCall emits one model-requested tool invocation.
func (l *Logger) EmitToolCall(callID, name string, args any) {
	l.emit(db.EvToolCall, db.ToolCallData{RunID: l.runID, WorkerID: l.workerID, CallID: callID, Name: name, Args: args})
}

// EmitToolResult emits the result of the tool call callID.
func (l *Logger) EmitToolResult(r db.ToolResultData) {
	r.RunID, r.WorkerID = l.runID, l.workerID
	l.emit(db.EvToolResult, r)
}

// EmitPullBegin starts a work unit on this lane (directory path, "N 个文件",
// instruction); count is the number of files pulled (0 if not applicable).
func (l *Logger) EmitPullBegin(label string, count int) {
	id := l.nextID("u")
	l.mu.Lock()
	l.unitID = id
	l.mu.Unlock()
	l.emit(db.EvPullBegin, db.PullBeginData{RunID: l.runID, WorkerID: l.workerID, UnitID: id, Label: label, Count: count})
}

// EmitPullDone settles the lane's current work unit.
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

// EmitProgress emits the run's absolute progress counters.
func (l *Logger) EmitProgress(p db.ProgressData) {
	p.RunID = l.runID
	l.emit(db.EvProgress, p)
}

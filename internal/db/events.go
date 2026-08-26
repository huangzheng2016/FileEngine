package db

import "sync"

// Session-scoped event bus for agent run events. All producers (agent logger,
// ADK event loop, orchestrator, executor, API handlers) publish here; the SSE
// endpoint (internal/api/handler_events.go) is the single subscriber side.
// The bus lives in the db package so every producer can reach it without
// import cycles (agent/api/executor all import db).

// EventType identifies the kind of a session run event. It is sent as the
// SSE `event:` field; the payload is JSON-marshaled as the `data:` field.
type EventType string

const (
	EventRunStatus  EventType = "run_status"
	EventPullBegin  EventType = "pull_begin"
	EventPullDone   EventType = "pull_done"
	EventMessage    EventType = "message"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventProgress   EventType = "progress"
	EventRunDone    EventType = "run_done"
)

// Run phases used by run_status / run_done / progress payloads.
const (
	PhaseTagging   = "tagging"
	PhaseInstruct  = "instruct"
	PhaseExecuting = "executing"
)

// Run statuses used by run_status / run_done payloads.
const (
	StatusStarted = "started"
	StatusDone    = "done"
	StatusError   = "error"
	StatusStopped = "stopped" // user-initiated stop (context canceled)
)

// Event is the internal bus envelope. SessionID routes the event to the
// session's subscribers; Payload is marshaled verbatim as the SSE data field.
type Event struct {
	Type      EventType
	SessionID uint
	Payload   interface{}
}

// RunStatusPayload mirrors ScanSession.Status transitions of a run phase.
type RunStatusPayload struct {
	SessionID uint   `json:"session_id"`
	Phase     string `json:"phase"`  // tagging / instruct / executing
	Status    string `json:"status"` // started / done / error / stopped
	Message   string `json:"message,omitempty"`
}

// PullBeginPayload announces a worker starting one work unit (one directory,
// one loose-file pull, or one instruct run). The batch system is gone: a
// batch index survives only as an internal DB cursor and is never exposed on
// the event stream — the frontend groups the timeline by worker lanes.
type PullBeginPayload struct {
	SessionID uint   `json:"session_id"`
	WorkerID  int    `json:"worker_id"`
	Label     string `json:"label"`            // directory path, "N files", or "instruct"
	Count     int    `json:"count,omitempty"`  // files pulled this round (loose-file pulls)
	Cursor    uint   `json:"cursor,omitempty"` // starting cursor (smallest file id of the pull)
}

// PullDonePayload settles the worker's current work unit: how many of its
// files ended up classified (tagged) versus missed or failed.
type PullDonePayload struct {
	SessionID uint   `json:"session_id"`
	WorkerID  int    `json:"worker_id"`
	Label     string `json:"label,omitempty"`
	Tagged    int    `json:"tagged,omitempty"`
	Failed    int    `json:"failed,omitempty"`
}

// MessagePayload carries a chat message. Persisted messages (user prompts,
// final assistant answers, system errors) include token counts; transient
// intermediate assistant steps omit them.
type MessagePayload struct {
	SessionID        uint   `json:"session_id"`
	WorkerID         int    `json:"worker_id"`
	Role             string `json:"role"` // user / assistant / system
	Content          string `json:"content"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CachedTokens     int    `json:"cached_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	TotalTokens      int    `json:"total_tokens,omitempty"`
}

// ToolCallPayload carries one model-requested tool invocation.
type ToolCallPayload struct {
	SessionID uint   `json:"session_id"`
	WorkerID  int    `json:"worker_id"`
	ToolName  string `json:"tool_name"`
	Input     string `json:"input"` // raw JSON arguments from the model
}

// ToolResultPayload carries one tool execution result.
type ToolResultPayload struct {
	SessionID uint   `json:"session_id"`
	WorkerID  int    `json:"worker_id"`
	ToolName  string `json:"tool_name"`
	Output    string `json:"output"` // tool output serialized as JSON text
	OK        bool   `json:"ok"`
}

// ProgressPayload carries absolute progress counters. Tagging runs fill
// Total/Tagged/Planned; executing runs fill Total/Success/Failed/Skipped.
type ProgressPayload struct {
	SessionID uint   `json:"session_id"`
	Phase     string `json:"phase"` // tagging / executing
	Total     int    `json:"total"`
	Tagged    int    `json:"tagged,omitempty"`
	Planned   int    `json:"planned,omitempty"`
	Success   int    `json:"success,omitempty"`
	Failed    int    `json:"failed,omitempty"`
	Skipped   int    `json:"skipped,omitempty"`
}

// RunDonePayload is the exactly-once terminal event of a run.
type RunDonePayload struct {
	SessionID uint   `json:"session_id"`
	Phase     string `json:"phase"`             // tagging / instruct / executing
	Status    string `json:"status"`            // done / error / stopped
	Summary   string `json:"summary,omitempty"` // tagging report / instruct response
}

const eventBufferSize = 256

var eventBus = struct {
	mu   sync.Mutex
	subs map[uint][]chan Event
}{subs: make(map[uint][]chan Event)}

// PublishEvent broadcasts ev to all current subscribers of ev.SessionID.
// Sends are non-blocking: a slow consumer drops events (history remains
// available via GET /logs), so producers never stall on a dead client.
func PublishEvent(ev Event) {
	eventBus.mu.Lock()
	chans := make([]chan Event, len(eventBus.subs[ev.SessionID]))
	copy(chans, eventBus.subs[ev.SessionID])
	eventBus.mu.Unlock()

	for _, ch := range chans {
		select {
		case ch <- ev:
		default:
		}
	}
}

// SubscribeEvents registers a buffered channel for a session's future events.
// The caller must pair it with UnsubscribeEvents. The channel is never closed
// by the bus; unsubscribing only detaches it.
func SubscribeEvents(sessionID uint) chan Event {
	ch := make(chan Event, eventBufferSize)
	eventBus.mu.Lock()
	eventBus.subs[sessionID] = append(eventBus.subs[sessionID], ch)
	eventBus.mu.Unlock()
	return ch
}

// UnsubscribeEvents detaches ch from the session's subscribers. The channel
// is deliberately not closed: a publisher may still hold a copied reference
// mid-broadcast, and sending on a closed channel would panic.
func UnsubscribeEvents(sessionID uint, ch chan Event) {
	eventBus.mu.Lock()
	defer eventBus.mu.Unlock()
	chans := eventBus.subs[sessionID]
	for i, c := range chans {
		if c == ch {
			eventBus.subs[sessionID] = append(chans[:i], chans[i+1:]...)
			break
		}
	}
	if len(eventBus.subs[sessionID]) == 0 {
		delete(eventBus.subs, sessionID)
	}
}

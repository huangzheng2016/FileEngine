package db

import (
	"encoding/json"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	dal "FileEngine/biz/dal/db"
	"FileEngine/biz/dal/db/model"
)

// Session event stream. Every event is persisted into session_events (the row
// id is the stream sequence number) and then broadcast to the session's live
// subscribers. Emit (runs.go) is the only producer entry point; the SSE
// endpoint (biz/handler/api/event_service.go) replays persisted rows and then
// follows the live broadcast.
//
// Text/reasoning deltas are coalesced per (run, worker): consecutive deltas
// of the same message and type accumulate in one pending chunk that is flushed
// after deltaFlushInterval or once it reaches deltaFlushBytes. A delta of a
// different message/type, and every non-delta event of the same run, flushes
// pending chunks first, so the persisted order always matches emission order.

const (
	deltaFlushInterval = 150 * time.Millisecond
	deltaFlushBytes    = 2048
	subscriberBuffer   = 512
)

// Subscriber is one live consumer of a session's events. When its buffer
// overflows the bus drops the event and raises Lagged; the consumer then
// re-reads the persisted rows after its last delivered seq.
type Subscriber struct {
	C         chan model.SessionEvent
	sessionID uint
	lagged    atomic.Bool
}

func (s *Subscriber) TakeLagged() bool { return s.lagged.Swap(false) }

type deltaKey struct {
	runID    uint
	workerID int
}

type pendingDelta struct {
	sessionID uint
	typ       string
	data      DeltaData
	timer     *time.Timer
	order     uint64
}

// sessionBus is one session's emit lane. Subscribers only need strictly
// increasing seq per session, so sessions never wait for each other's
// database writes.
type sessionBus struct {
	// mu serializes persist+broadcast of this session's events.
	mu      sync.Mutex
	pending map[deltaKey]*pendingDelta
	deltaN  uint64
}

var bus = struct {
	mu       sync.Mutex
	sessions map[uint]*sessionBus
	// memSeq numbers events when no database is open (unit tests).
	memSeq atomic.Uint64

	subMu sync.Mutex
	subs  map[uint][]*Subscriber
}{
	sessions: make(map[uint]*sessionBus),
	subs:     make(map[uint][]*Subscriber),
}

func laneOf(sessionID uint) *sessionBus {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	l := bus.sessions[sessionID]
	if l == nil {
		l = &sessionBus{pending: make(map[deltaKey]*pendingDelta)}
		bus.sessions[sessionID] = l
	}
	return l
}

func emitEvent(sessionID, runID uint, typ string, data any) {
	l := laneOf(sessionID)
	l.mu.Lock()
	defer l.mu.Unlock()

	if typ == EvText || typ == EvReasoning {
		d, ok := asDelta(data)
		if !ok {
			return
		}
		key := deltaKey{runID: runID, workerID: d.WorkerID}
		if p := l.pending[key]; p != nil {
			if p.typ == typ && p.data.MsgID == d.MsgID {
				p.data.Delta += d.Delta
				if len(p.data.Delta) >= deltaFlushBytes {
					l.flushLocked(sessionID, key)
				}
				return
			}
			l.flushLocked(sessionID, key)
		}
		d.RunID = runID
		l.deltaN++
		p := &pendingDelta{sessionID: sessionID, typ: typ, data: d, order: l.deltaN}
		l.pending[key] = p
		p.timer = time.AfterFunc(deltaFlushInterval, func() { flushTimer(sessionID, key, p) })
		if len(d.Delta) >= deltaFlushBytes {
			l.flushLocked(sessionID, key)
		}
		return
	}

	l.flushRunLocked(sessionID, runID)
	raw, err := json.Marshal(data)
	if err != nil {
		log.Printf("events: marshal %s: %v", typ, err)
		return
	}
	persistAndBroadcastLocked(sessionID, runID, typ, string(raw))
}

func asDelta(data any) (DeltaData, bool) {
	switch d := data.(type) {
	case DeltaData:
		return d, true
	case *DeltaData:
		if d != nil {
			return *d, true
		}
	}
	return DeltaData{}, false
}

func flushTimer(sessionID uint, key deltaKey, p *pendingDelta) {
	l := laneOf(sessionID)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending[key] == p {
		l.flushLocked(sessionID, key)
	}
}

// flushLocked persists the pending delta chunk of key. Caller holds l.mu.
func (l *sessionBus) flushLocked(sessionID uint, key deltaKey) {
	p := l.pending[key]
	if p == nil {
		return
	}
	delete(l.pending, key)
	p.timer.Stop()
	if p.data.Delta == "" {
		return
	}
	raw, err := json.Marshal(p.data)
	if err != nil {
		return
	}
	persistAndBroadcastLocked(sessionID, key.runID, p.typ, string(raw))
}

// flushRunLocked persists the pending chunks of runID (all runs of the session
// when 0) in the order they were started. Caller holds l.mu.
func (l *sessionBus) flushRunLocked(sessionID, runID uint) {
	var keys []deltaKey
	for key := range l.pending {
		if runID == 0 || key.runID == runID {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return l.pending[keys[i]].order < l.pending[keys[j]].order })
	for _, key := range keys {
		l.flushLocked(sessionID, key)
	}
}

// FlushEvents persists every pending delta chunk of every session.
func FlushEvents() {
	bus.mu.Lock()
	ids := make([]uint, 0, len(bus.sessions))
	for id := range bus.sessions {
		ids = append(ids, id)
	}
	bus.mu.Unlock()
	for _, id := range ids {
		l := laneOf(id)
		l.mu.Lock()
		l.flushRunLocked(id, 0)
		l.mu.Unlock()
	}
}

// Without a database (unit tests that never call Init) the seq comes from an
// in-memory counter. The caller holds the session's lane lock.
func persistAndBroadcastLocked(sessionID, runID uint, typ, data string) {
	ev := model.SessionEvent{SessionID: sessionID, RunID: runID, Type: typ, Data: data, CreatedAt: time.Now()}
	if g := dal.DB(); g != nil {
		if err := dal.WithRetry(func() error { return g.Create(&ev).Error }); err != nil {
			log.Printf("events: persist %s for session %d: %v", typ, sessionID, err)
			return
		}
	} else {
		ev.ID = uint(bus.memSeq.Add(1))
	}
	broadcast(ev)
}

func broadcast(ev model.SessionEvent) {
	bus.subMu.Lock()
	subs := append([]*Subscriber(nil), bus.subs[ev.SessionID]...)
	bus.subMu.Unlock()
	for _, s := range subs {
		select {
		case s.C <- ev:
		default:
			s.lagged.Store(true)
		}
	}
}

// The channel is never closed by the bus.
func Subscribe(sessionID uint) *Subscriber {
	s := &Subscriber{C: make(chan model.SessionEvent, subscriberBuffer), sessionID: sessionID}
	bus.subMu.Lock()
	bus.subs[sessionID] = append(bus.subs[sessionID], s)
	bus.subMu.Unlock()
	return s
}

// Unsubscribe detaches s. The channel stays open: a broadcaster may still
// hold a copied reference.
func Unsubscribe(s *Subscriber) {
	bus.subMu.Lock()
	defer bus.subMu.Unlock()
	list := bus.subs[s.sessionID]
	for i, c := range list {
		if c == s {
			bus.subs[s.sessionID] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(bus.subs[s.sessionID]) == 0 {
		delete(bus.subs, s.sessionID)
	}
}

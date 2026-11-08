package db

import (
	"encoding/json"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

// Session event stream. Every event is persisted into session_events (the row
// id is the stream sequence number) and then broadcast to the session's live
// subscribers. Emit (runs.go) is the only producer entry point; the SSE
// endpoint (internal/api/handler_events.go) replays persisted rows and then
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
	C         chan SessionEvent
	sessionID uint
	lagged    atomic.Bool
}

// TakeLagged reports (and clears) whether events were dropped for s.
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

var bus = struct {
	// emitMu serializes persist+broadcast so subscribers observe events in
	// strictly increasing seq order.
	emitMu  sync.Mutex
	pending map[deltaKey]*pendingDelta
	memSeq  uint
	deltaN  uint64

	subMu sync.Mutex
	subs  map[uint][]*Subscriber
}{
	pending: make(map[deltaKey]*pendingDelta),
	subs:    make(map[uint][]*Subscriber),
}

// emitEvent is the body of Emit.
func emitEvent(sessionID, runID uint, typ string, data any) {
	bus.emitMu.Lock()
	defer bus.emitMu.Unlock()

	if typ == EvText || typ == EvReasoning {
		d, ok := asDelta(data)
		if !ok {
			return
		}
		key := deltaKey{runID: runID, workerID: d.WorkerID}
		if p := bus.pending[key]; p != nil {
			if p.typ == typ && p.data.MsgID == d.MsgID {
				p.data.Delta += d.Delta
				if len(p.data.Delta) >= deltaFlushBytes {
					flushLocked(key)
				}
				return
			}
			flushLocked(key)
		}
		d.RunID = runID
		bus.deltaN++
		p := &pendingDelta{sessionID: sessionID, typ: typ, data: d, order: bus.deltaN}
		bus.pending[key] = p
		p.timer = time.AfterFunc(deltaFlushInterval, func() { flushTimer(key, p) })
		if len(d.Delta) >= deltaFlushBytes {
			flushLocked(key)
		}
		return
	}

	flushRunLocked(runID)
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

func flushTimer(key deltaKey, p *pendingDelta) {
	bus.emitMu.Lock()
	defer bus.emitMu.Unlock()
	if bus.pending[key] == p {
		flushLocked(key)
	}
}

// flushLocked persists the pending delta chunk of key. Caller holds emitMu.
func flushLocked(key deltaKey) {
	p := bus.pending[key]
	if p == nil {
		return
	}
	delete(bus.pending, key)
	p.timer.Stop()
	if p.data.Delta == "" {
		return
	}
	raw, err := json.Marshal(p.data)
	if err != nil {
		return
	}
	persistAndBroadcastLocked(p.sessionID, key.runID, p.typ, string(raw))
}

// flushRunLocked persists the pending chunks of runID (all runs when 0) in
// the order they were started. Caller holds emitMu.
func flushRunLocked(runID uint) {
	var keys []deltaKey
	for key := range bus.pending {
		if runID == 0 || key.runID == runID {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return bus.pending[keys[i]].order < bus.pending[keys[j]].order })
	for _, key := range keys {
		flushLocked(key)
	}
}

// FlushEvents persists every pending delta chunk immediately.
func FlushEvents() {
	bus.emitMu.Lock()
	defer bus.emitMu.Unlock()
	flushRunLocked(0)
}

// persistAndBroadcastLocked stores one event row and fans it out. Without a
// database (unit tests that never call Init) the seq comes from an in-memory
// counter and nothing is persisted.
func persistAndBroadcastLocked(sessionID, runID uint, typ, data string) {
	ev := SessionEvent{SessionID: sessionID, RunID: runID, Type: typ, Data: data, CreatedAt: time.Now()}
	if DB != nil {
		if err := WithRetry(func() error { return DB.Create(&ev).Error }); err != nil {
			log.Printf("events: persist %s for session %d: %v", typ, sessionID, err)
			return
		}
	} else {
		bus.memSeq++
		ev.ID = bus.memSeq
	}
	broadcast(ev)
}

func broadcast(ev SessionEvent) {
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

// Subscribe registers a live consumer of sessionID's events; pair it with
// Unsubscribe. The channel is never closed by the bus.
func Subscribe(sessionID uint) *Subscriber {
	s := &Subscriber{C: make(chan SessionEvent, subscriberBuffer), sessionID: sessionID}
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

// EventQuery selects persisted events of one session in ascending seq order.
type EventQuery struct {
	SessionID uint
	RunID     uint // 0 = all runs
	After     uint // seq > After
	Before    uint // seq < Before (0 = no bound)
	Limit     int
}

// ListEvents returns up to q.Limit events and whether more exist past them.
// With only Before set it returns the newest events below Before (still in
// ascending order), which is the backwards-paging shape.
func (r *Repository) ListEvents(q EventQuery) ([]SessionEvent, bool, error) {
	if q.Limit <= 0 || q.Limit > 1000 {
		q.Limit = 500
	}
	tx := r.db.Model(&SessionEvent{}).Where("session_id = ?", q.SessionID)
	if q.RunID > 0 {
		tx = tx.Where("run_id = ?", q.RunID)
	}
	if q.After > 0 {
		tx = tx.Where("id > ?", q.After)
	}
	if q.Before > 0 {
		tx = tx.Where("id < ?", q.Before)
	}
	desc := q.Before > 0 && q.After == 0
	order := "id ASC"
	if desc {
		order = "id DESC"
	}
	var rows []SessionEvent
	if err := tx.Order(order).Limit(q.Limit + 1).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	more := len(rows) > q.Limit
	if more {
		rows = rows[:q.Limit]
	}
	if desc {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}
	return rows, more, nil
}

// LatestRunStartSeq returns the seq just before the first event of the
// session's latest run (0 when the session has no run events) — the default
// replay origin of the SSE stream.
func (r *Repository) LatestRunStartSeq(sessionID uint) (uint, error) {
	var run AgentRun
	err := r.db.Where("session_id = ?", sessionID).Order("id DESC").First(&run).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var first *uint
	if err := r.db.Model(&SessionEvent{}).Where("session_id = ? AND run_id = ?", sessionID, run.ID).
		Select("MIN(id)").Scan(&first).Error; err != nil {
		return 0, err
	}
	if first == nil || *first == 0 {
		return 0, nil
	}
	return *first - 1, nil
}

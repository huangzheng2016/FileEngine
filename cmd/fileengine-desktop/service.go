package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"FileEngine/internal/app"
	"FileEngine/internal/db"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Service is the one Wails service of the desktop. The window reaches the
// backend through it only: Call runs a request through the same Hertz routes
// as the web server (so the IDL is the only route table), and Subscribe
// forwards a session's events as Wails events. There is no listener, no token
// and no cookie: both bindings are in-process and trusted.
type Service struct {
	app *app.App
	// emit publishes a Wails event; nil uses the running application.
	emit func(topic string, payload any)

	nextSub atomic.Uint64
	mu      sync.Mutex
	subs    map[string]func()
}

func (s *Service) ServiceName() string {
	return "FileEngine"
}

// serviceShutdownTimeout bounds the drain of subscriptions, runs and scans.
// Wails runs ServiceShutdown on the main thread, so it must not hang.
const serviceShutdownTimeout = 5 * time.Second

func (s *Service) ServiceShutdown() error {
	if s.app == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), serviceShutdownTimeout)
	defer cancel()
	return s.app.Shutdown(ctx)
}

// CallRequest mirrors an HTTP request to an /api route.
type CallRequest struct {
	Method string `json:"method"`
	// Path is the route, for example /api/session/list.
	Path string `json:"path"`
	// Query holds the query parameters of a GET.
	Query map[string]string `json:"query,omitempty"`
	// Body is the JSON text of a POST body.
	Body string `json:"body,omitempty"`
}

// CallResponse mirrors the HTTP response. Body is the JSON text for JSON and
// text responses (Encoding "utf8") and base64 for downloads (Encoding "base64").
type CallResponse struct {
	Status             int    `json:"status"`
	ContentType        string `json:"content_type"`
	ContentDisposition string `json:"content_disposition,omitempty"`
	Encoding           string `json:"encoding"`
	Body               string `json:"body"`
}

// Call dispatches one request. Failures are normal responses carrying the
// {code, msg} envelope; the SSE route is refused (use Subscribe).
func (s *Service) Call(ctx context.Context, req CallRequest) CallResponse {
	res := s.app.Do(ctx, app.Call{Method: strings.ToUpper(req.Method), Path: req.Path, Query: req.Query, Body: []byte(req.Body)})
	out := CallResponse{Status: res.Status, ContentType: res.ContentType, ContentDisposition: res.ContentDisposition, Encoding: "utf8"}
	if isTextual(res.ContentType) {
		out.Body = string(res.Body)
	} else {
		out.Encoding = "base64"
		out.Body = base64.StdEncoding.EncodeToString(res.Body)
	}
	return out
}

func isTextual(contentType string) bool {
	ct := strings.ToLower(contentType)
	return ct == "" || strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/")
}

type SubscribeRequest struct {
	// SessionID is the session's id as the API returns it (a string).
	SessionID string `json:"session_id"`
	// RunID limits the feed to one run; empty = every run.
	RunID string `json:"run_id,omitempty"`
	// After resumes after this seq; null starts at the latest run's first event.
	After *int64 `json:"after"`
}

type SubscribeResponse struct {
	SubscriptionID string `json:"subscription_id"`
	// Topic carries the events; Topic + "/end" signals that the feed stopped.
	Topic string `json:"topic"`
}

// EventPayload is the Wails event payload of the events topic: one session
// event, identical to the event.SessionEvent of /api/event/list and to an SSE
// frame (seq = id, kind = event, data = the data JSON text).
type EventPayload struct {
	Seq       int64  `json:"seq"`
	RunID     string `json:"run_id"`
	Kind      string `json:"kind"`
	Data      string `json:"data"`
	CreatedAt int64  `json:"created_at"`
}

// EndPayload is the payload of the end topic.
type EndPayload struct {
	SubscriptionID string `json:"subscription_id"`
	// Error is empty after Unsubscribe and shutdown.
	Error string `json:"error,omitempty"`
}

func eventsTopic(sessionID string) string { return "session://events/" + sessionID }

// Subscribe starts forwarding a session's events (history after `after`, then
// live, in order and without gaps) as Wails events on the returned topic.
// Subscribing again after a reconnect with the last seen seq resumes exactly.
func (s *Service) Subscribe(req SubscribeRequest) (SubscribeResponse, error) {
	sid, err := strconv.ParseUint(req.SessionID, 10, 64)
	if err != nil || sid == 0 {
		return SubscribeResponse{}, errors.New("会话编号无效")
	}
	var runID uint
	if req.RunID != "" {
		n, err := strconv.ParseUint(req.RunID, 10, 64)
		if err != nil {
			return SubscribeResponse{}, errors.New("运行编号无效")
		}
		runID = uint(n)
	}
	var after *uint
	if req.After != nil {
		if *req.After < 0 {
			return SubscribeResponse{}, errors.New("事件序号无效")
		}
		u := uint(*req.After)
		after = &u
	}
	id := "sub-" + strconv.FormatUint(s.nextSub.Add(1), 10)
	topic := eventsTopic(req.SessionID)
	emit := s.emitter()
	stop, err := s.app.Follow(uint(sid), runID, after,
		func(ev db.SessionEvent) error {
			emit(topic, EventPayload{Seq: int64(ev.ID), RunID: strconv.FormatUint(uint64(ev.RunID), 10),
				Kind: ev.Type, Data: ev.Data, CreatedAt: ev.CreatedAt.Unix()})
			return nil
		},
		func(err error) {
			s.mu.Lock()
			delete(s.subs, id)
			s.mu.Unlock()
			end := EndPayload{SubscriptionID: id}
			if err != nil {
				end.Error = err.Error()
			}
			emit(topic+"/end", end)
		})
	if err != nil {
		return SubscribeResponse{}, err
	}
	s.mu.Lock()
	if s.subs == nil {
		s.subs = make(map[string]func())
	}
	s.subs[id] = stop
	s.mu.Unlock()
	return SubscribeResponse{SubscriptionID: id, Topic: topic}, nil
}

// Unsubscribe stops a subscription; unknown ids are ignored.
func (s *Service) Unsubscribe(subscriptionID string) {
	s.mu.Lock()
	stop := s.subs[subscriptionID]
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func (s *Service) emitter() func(string, any) {
	if s.emit != nil {
		return s.emit
	}
	return func(topic string, payload any) { application.Get().Event.Emit(topic, payload) }
}

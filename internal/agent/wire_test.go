package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"FileEngine/biz/dal/db/model"
	"FileEngine/internal/config"
	"FileEngine/internal/db"
	modelfactory "FileEngine/internal/model"
	"FileEngine/internal/remotefs"
)

// A fake Claude Messages API server: it records every request body exactly as
// sent and answers with a scripted SSE stream, so tests assert on the bytes a
// real provider would see. No credentials, no network beyond loopback.

type wireCall struct {
	id, name, args string
}

type wireReply struct {
	text  string
	calls []wireCall
}

type wireReq struct {
	raw      []byte
	tools    json.RawMessage
	system   json.RawMessage
	messages []json.RawMessage
}

func parseWireReq(t *testing.T, raw []byte) (wireReq, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return wireReq{}, err
	}
	r := wireReq{raw: raw, tools: top["tools"], system: top["system"]}
	if err := json.Unmarshal(top["messages"], &r.messages); err != nil {
		return wireReq{}, err
	}
	return r, nil
}

type wireServer struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	reqs    []wireReq
	bad     int // request bodies that were not valid JSON
	respond func(r wireReq) wireReply
}

func newWireServer(t *testing.T, respond func(r wireReq) wireReply) *wireServer {
	ws := &wireServer{t: t, respond: respond}
	ws.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req, err := parseWireReq(t, body)
		ws.mu.Lock()
		if err != nil {
			ws.bad++
			ws.mu.Unlock()
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		ws.reqs = append(ws.reqs, req)
		ws.mu.Unlock()
		writeSSE(w, ws.respond(req))
	}))
	t.Cleanup(ws.srv.Close)
	return ws
}

func (ws *wireServer) requests() []wireReq {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return append([]wireReq(nil), ws.reqs...)
}

func writeSSE(w http.ResponseWriter, rep wireReply) {
	w.Header().Set("Content-Type", "text/event-stream")
	ev := func(name string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
	}
	ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "m", "content": []any{},
		"stop_reason": nil, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1},
	}})
	idx := 0
	if rep.text != "" {
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "text", "text": ""}})
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "text_delta", "text": rep.text}})
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
		idx++
	}
	for _, c := range rep.calls {
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "tool_use", "id": c.id, "name": c.name, "input": map[string]any{}}})
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "input_json_delta", "partial_json": c.args}})
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
		idx++
	}
	stop := "end_turn"
	if len(rep.calls) > 0 {
		stop = "tool_use"
	}
	ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": 5}})
	ev("message_stop", map[string]any{"type": "message_stop"})
}

// stripCacheControl canonicalizes a JSON value without its cache_control
// markers, which move with the rolling breakpoint.
func stripCacheControl(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var walk func(any) any
	walk = func(x any) any {
		switch y := x.(type) {
		case map[string]any:
			delete(y, "cache_control")
			for k, e := range y {
				y[k] = walk(e)
			}
		case []any:
			for i := range y {
				y[i] = walk(y[i])
			}
		}
		return x
	}
	out, err := json.Marshal(walk(v))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func assistantCount(r wireReq) int {
	n := 0
	for _, m := range r.messages {
		var head struct {
			Role string `json:"role"`
		}
		_ = json.Unmarshal(m, &head)
		if head.Role == "assistant" {
			n++
		}
	}
	return n
}

// toolResults returns the tool_result contents of the last user message.
func toolResults(t *testing.T, r wireReq) []string {
	t.Helper()
	var last struct {
		Content []struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(r.messages[len(r.messages)-1], &last); err != nil {
		t.Fatalf("last message: %v", err)
	}
	var out []string
	for _, b := range last.Content {
		if b.Type != "tool_result" {
			continue
		}
		var sb strings.Builder
		for _, c := range b.Content {
			sb.WriteString(c.Text)
		}
		out = append(out, sb.String())
	}
	return out
}

func claudeProvider(url string) *model.ModelProvider {
	return &model.ModelProvider{Provider: "claude", Model: "claude-opus-5-5", APIKey: "x", BaseURL: url, MaxTokens: 4096}
}

// wireAgent is a tagging run over the pipeline fixture whose model is the
// fake server, with a local filesystem root for read_file.
func wireAgent(t *testing.T, ws *wireServer, mutate func(*model.ScanSession)) (*AutoTagAgent, *db.Repository, string) {
	t.Helper()
	a, repo := pipelineFixture(t, "照片按年份整理")
	root := t.TempDir()
	if err := remotefs.SetLocalPolicy(remotefs.LocalPolicy{Roots: []string{root}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remotefs.SetLocalPolicy(remotefs.LocalPolicy{}) })
	old := config.Get()
	config.Update(&config.Config{Agent: config.AgentConfig{MaxStep: 20, ChatMaxStep: 20, MaxRetries: 1, Concurrency: 2}})
	t.Cleanup(func() {
		if old != nil {
			config.Update(old)
		}
	})
	a.fsCfg = config.RemoteFSConfig{Protocol: "local", BasePath: root}
	a.modelProvider = claudeProvider(ws.srv.URL)
	a.session.AllowReadFile = true
	if mutate != nil {
		mutate(a.session)
	}
	return a, repo, root
}

func newWireWorker(t *testing.T, a *AutoTagAgent, id int) *tagWorker {
	t.Helper()
	m, err := modelfactory.NewChatModelFromProvider(context.Background(), a.modelProvider)
	if err != nil {
		t.Fatal(err)
	}
	w, closeFn, err := a.newWorker(context.Background(), id, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeFn)
	return w
}

// firstRequests are the requests that open a unit: only the shared snapshot
// and the unit message precede them.
func firstRequests(reqs []wireReq) []wireReq {
	var out []wireReq
	for _, r := range reqs {
		if len(r.messages) == 2 {
			out = append(out, r)
		}
	}
	return out
}

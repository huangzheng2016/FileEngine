package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"FileEngine/biz/actor"
	"FileEngine/biz/errs"

	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/route"
)

// Call is one HTTP-shaped request dispatched in-process.
type Call struct {
	Method string
	// Path is an /api path without query.
	Path  string
	Query map[string]string
	// Body is the JSON request body, empty for none.
	Body []byte
}

type Result struct {
	Status             int
	ContentType        string
	ContentDisposition string
	Body               []byte
}

const streamPath = "/api/event/stream"

// Do runs call through the same engine and routes as the network server. The
// context is marked as a trusted desktop call, which only Go code can do, so
// default-deny auth passes without a token. Only /api routes are reachable, and
// the SSE stream is refused: the desktop follows events through a subscription.
func (a *App) Do(ctx context.Context, call Call) Result {
	if call.Path != "/api" && !strings.HasPrefix(call.Path, "/api/") {
		return envelope(errs.NoSuchRoute)
	}
	if call.Path == streamPath {
		return envelope(errs.Invalid("桌面版请使用事件订阅"))
	}
	h := a.Engine()
	rc := h.NewContext()
	uri := call.Path
	if len(call.Query) > 0 {
		q := url.Values{}
		for k, v := range call.Query {
			q.Set(k, v)
		}
		uri += "?" + q.Encode()
	}
	var req *protocol.Request
	if len(call.Body) > 0 {
		req = protocol.NewRequest(call.Method, uri, bytes.NewReader(call.Body))
		req.SetBody(call.Body)
		req.Header.SetContentTypeBytes([]byte("application/json"))
	} else {
		req = protocol.NewRequest(call.Method, uri, nil)
	}
	req.CopyTo(&rc.Request)
	rc.Request.Header.SetHost("fileengine.desktop")
	h.ServeHTTP(actor.WithDesktop(ctx), rc)
	return Result{
		Status:             rc.Response.StatusCode(),
		ContentType:        string(rc.Response.Header.ContentType()),
		ContentDisposition: string(rc.Response.Header.Peek("Content-Disposition")),
		Body:               append([]byte(nil), rc.Response.Body()...),
	}
}

func envelope(e *errs.Error) Result {
	body, _ := json.Marshal(map[string]any{"code": e.Code, "msg": e.Msg})
	return Result{Status: e.Status, ContentType: "application/json; charset=utf-8", Body: body}
}

// Engine is the engine behind Do (and behind hertz ut tests). It never listens.
func (a *App) Engine() *route.Engine {
	a.inprocOnce.Do(func() { a.inproc = a.newHertz().Engine })
	return a.inproc
}

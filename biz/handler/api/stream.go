package api

import (
	"strconv"
	"time"

	event "FileEngine/biz/model/api/event"
	"FileEngine/internal/db"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/protocol/http1/resp"
	"github.com/hertz-contrib/sse"
)

// SSEHeartbeat is how often a stream writes a comment frame. A write is the
// only way to notice a client that disappeared without closing: Hertz cannot
// sense disconnects on Windows.
var SSEHeartbeat = 20 * time.Second

type sseSink struct {
	w      network.ExtWriter
	stream *sse.Stream
}

func newSSESink(c *app.RequestContext) *sseSink {
	// tells reverse proxies not to buffer the stream
	c.Response.Header.Set("X-Accel-Buffering", "no")
	w := resp.NewChunkedBodyWriter(&c.Response, c.GetWriter())
	return &sseSink{w: w, stream: sse.NewStreamWithWriter(c, w)}
}

func (s *sseSink) Event(ev db.SessionEvent) error {
	return s.stream.Publish(&sse.Event{ID: strconv.FormatUint(uint64(ev.ID), 10), Event: ev.Type, Data: []byte(ev.Data)})
}

func (s *sseSink) Ping() error { return s.comment("ping") }

func (s *sseSink) comment(text string) error {
	if _, err := s.w.Write([]byte(": " + text + "\n\n")); err != nil {
		return err
	}
	return s.w.Flush()
}

func toSessionEvents(rows []db.SessionEvent) []*event.SessionEvent {
	items := make([]*event.SessionEvent, len(rows))
	for i, ev := range rows {
		items[i] = &event.SessionEvent{Seq: int64(ev.ID), RunID: int64(ev.RunID), Kind: ev.Type, Data: ev.Data, CreatedAt: ev.CreatedAt.Unix()}
	}
	return items
}

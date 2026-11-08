package main

import (
	"net/http"
	"testing"
)

// TestLoopbackStreamsIncrementally pins the reason for the loopback design:
// an SSE event written and flushed reaches the client before the handler
// returns.
func TestLoopbackStreamsIncrementally(t *testing.T) {
	release := make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: ping\ndata: {}\n\n"))
		w.(http.Flusher).Flush()
		<-release
	})
	ln, err := listenLoopback()
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: next}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	defer close(release)

	resp, err := http.Get("http://" + ln.Addr().String() + "/api/v1/sessions/1/events/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	n, err := resp.Body.Read(buf)
	if err != nil || string(buf[:n]) != "event: ping\ndata: {}\n\n" {
		t.Fatalf("first chunk %q err=%v", buf[:n], err)
	}
}

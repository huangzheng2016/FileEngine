package db

import (
	"testing"
	"time"
)

func recvEvent(t *testing.T, ch chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func assertNoEvent(t *testing.T, ch chan Event) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventBusPublishSubscribe(t *testing.T) {
	ch := SubscribeEvents(42)
	defer UnsubscribeEvents(42, ch)

	PublishEvent(Event{Type: EventRunStatus, SessionID: 42, Payload: RunStatusPayload{
		SessionID: 42, Phase: PhaseTagging, Status: StatusStarted,
	}})

	ev := recvEvent(t, ch)
	if ev.Type != EventRunStatus {
		t.Fatalf("event type: got %q, want %q", ev.Type, EventRunStatus)
	}
	p, ok := ev.Payload.(RunStatusPayload)
	if !ok {
		t.Fatalf("payload type: got %T, want RunStatusPayload", ev.Payload)
	}
	if p.Phase != PhaseTagging || p.Status != StatusStarted {
		t.Fatalf("payload: %+v", p)
	}
}

func TestEventBusSessionIsolation(t *testing.T) {
	ch1 := SubscribeEvents(1)
	defer UnsubscribeEvents(1, ch1)
	ch2 := SubscribeEvents(2)
	defer UnsubscribeEvents(2, ch2)

	PublishEvent(Event{Type: EventProgress, SessionID: 1, Payload: ProgressPayload{SessionID: 1}})

	recvEvent(t, ch1)
	assertNoEvent(t, ch2)
}

func TestEventBusUnsubscribe(t *testing.T) {
	ch := SubscribeEvents(7)
	UnsubscribeEvents(7, ch)

	PublishEvent(Event{Type: EventRunDone, SessionID: 7, Payload: RunDonePayload{SessionID: 7}})
	assertNoEvent(t, ch)
}

func TestEventBusSlowConsumerDoesNotBlock(t *testing.T) {
	ch := SubscribeEvents(9)
	defer UnsubscribeEvents(9, ch)

	// Fill the buffer far beyond capacity without draining; PublishEvent must
	// drop instead of blocking the producer.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < eventBufferSize*3; i++ {
			PublishEvent(Event{Type: EventMessage, SessionID: 9, Payload: MessagePayload{SessionID: 9}})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PublishEvent blocked on a slow consumer")
	}
}

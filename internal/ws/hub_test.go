package ws

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestClient(h *Hub, buf int) *Client {
	return &Client{Hub: h, Send: make(chan []byte, buf), UserID: uuid.New()}
}

func register(t *testing.T, h *Hub, c *Client) {
	t.Helper()
	h.Register <- c
	deadline := time.Now().Add(time.Second)
	for !h.CheckWsConnection(c.UserID) {
		if time.Now().After(deadline) {
			t.Fatal("client not registered")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWriteToUsers(t *testing.T) {
	h := NewHub()
	go h.Run()
	a, b := newTestClient(h, 4), newTestClient(h, 4)
	register(t, h, a)
	register(t, h, b)

	if err := h.WriteToUsers([]uuid.UUID{a.UserID, uuid.New()}, Event{Event: EventMessage, Payload: "hi"}); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-a.Send:
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil || ev.Event != EventMessage {
			t.Fatalf("got %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("a did not receive the event")
	}
	if len(b.Send) != 0 {
		t.Fatal("b should not receive a's event")
	}
}

func TestSlowClientIsDisconnected(t *testing.T) {
	h := NewHub()
	go h.Run()
	c := newTestClient(h, 1)
	register(t, h, c)
	_ = h.WriteIntoConnection(c.UserID, Event{Event: "x"})
	_ = h.WriteIntoConnection(c.UserID, Event{Event: "x"}) // buffer full
	if h.CheckWsConnection(c.UserID) {
		t.Fatal("slow client should be dropped")
	}
	// Send is closed exactly once; draining must not panic
	for range c.Send {
	}
}

func TestReconnectReplacesOldConnection(t *testing.T) {
	h := NewHub()
	go h.Run()
	old := newTestClient(h, 1)
	register(t, h, old)
	fresh := &Client{Hub: h, Send: make(chan []byte, 1), UserID: old.UserID}
	h.Register <- fresh
	// old connection's channel is closed
	select {
	case _, ok := <-old.Send:
		if ok {
			t.Fatal("expected old Send to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("old connection not closed")
	}
	// the old client unregistering must not remove the new one
	h.Unregister <- old
	time.Sleep(10 * time.Millisecond)
	if !h.CheckWsConnection(old.UserID) {
		t.Fatal("stale unregister removed the new connection")
	}
}

package ws

import (
	"encoding/json"
	"log/slog"
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
	h := NewHub(slog.New(slog.DiscardHandler))
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
	h := NewHub(slog.New(slog.DiscardHandler))
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

func TestSeveralDevicesAllReceive(t *testing.T) {
	h := NewHub(slog.New(slog.DiscardHandler))
	go h.Run()
	phone := newTestClient(h, 4)
	register(t, h, phone)
	tablet := &Client{Hub: h, Send: make(chan []byte, 4), UserID: phone.UserID}
	h.Register <- tablet
	time.Sleep(10 * time.Millisecond)
	if err := h.WriteToUsers([]uuid.UUID{phone.UserID}, Event{Event: "msg"}); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*Client{"phone": phone, "tablet": tablet} {
		select {
		case <-c.Send:
		case <-time.After(time.Second):
			t.Fatalf("%s got nothing", name)
		}
	}
	// one device leaving keeps the user online
	h.Unregister <- phone
	time.Sleep(10 * time.Millisecond)
	if !h.Online(phone.UserID) {
		t.Fatal("user offline although the tablet is connected")
	}
	if h.Count() != 1 {
		t.Fatalf("count %d", h.Count())
	}
	// disconnecting the user closes every device
	h.Disconnect(phone.UserID)
	if _, ok := <-tablet.Send; ok || h.Online(phone.UserID) {
		t.Fatal("tablet still connected after Disconnect")
	}
}

func TestTooManyDevicesDropsTheOldest(t *testing.T) {
	h := NewHub(slog.New(slog.DiscardHandler))
	go h.Run()
	first := newTestClient(h, 1)
	register(t, h, first)
	for range MaxDevices {
		h.Register <- &Client{Hub: h, Send: make(chan []byte, 1), UserID: first.UserID}
	}
	select {
	case _, ok := <-first.Send:
		if ok {
			t.Fatal("expected the oldest Send to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("oldest connection not closed")
	}
	// its late unregister must not touch the others
	h.Unregister <- first
	time.Sleep(10 * time.Millisecond)
	h.mu.RLock()
	n := len(h.clients[first.UserID])
	h.mu.RUnlock()
	if n != MaxDevices {
		t.Fatalf("%d connections, want %d", n, MaxDevices)
	}
}

func TestTypingIsThrottledPerChat(t *testing.T) {
	c := &Client{}
	a, b := uuid.New(), uuid.New()
	now := time.Now()
	if !c.allowTyping(a, now) {
		t.Fatal("first typing event must pass")
	}
	if c.allowTyping(a, now.Add(time.Second)) {
		t.Fatal("a second event within the interval must be dropped")
	}
	if !c.allowTyping(b, now.Add(time.Second)) {
		t.Fatal("other chats have their own budget")
	}
	if !c.allowTyping(a, now.Add(typingInterval)) {
		t.Fatal("after the interval typing passes again")
	}
}

func TestPresenceHookFirstAndLastDevice(t *testing.T) {
	h := NewHub(slog.New(slog.DiscardHandler))
	got := make(chan bool, 10)
	h.OnPresence(func(_ uuid.UUID, online bool) { got <- online })
	go h.Run()
	phone := newTestClient(h, 4)
	tablet := &Client{Hub: h, Send: make(chan []byte, 4), UserID: phone.UserID}
	register(t, h, phone)
	h.Register <- tablet
	if v := <-got; !v {
		t.Fatal("first device should report online")
	}
	h.Unregister <- phone
	h.Unregister <- tablet
	if v := <-got; v {
		t.Fatal("last device leaving should report offline")
	}
	select {
	case v := <-got:
		t.Fatalf("extra presence change %v", v)
	case <-time.After(50 * time.Millisecond):
	}
}

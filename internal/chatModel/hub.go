package chatmodel

import (
	"encoding/json"
	"errors"
	"log"
	"sync"

	"github.com/google/uuid"
)

// Hub tracks live websocket clients and routes events to them.
// The clients map is guarded by mu; every method is safe for concurrent use.
type Hub struct {
	mu      sync.RWMutex
	clients map[uuid.UUID]*Client

	Register   chan *Client
	Unregister chan *Client
	// Broadcast carries client-originated events (typing, seen).
	Broadcast chan Event
}

var ErrNoClientFound = errors.New("no client exist")

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[uuid.UUID]*Client),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Broadcast:  make(chan Event, 1000),
	}
}

func (h *Hub) CheckWsConnection(clientID uuid.UUID) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.clients[clientID]
	return ok
}

// WriteIntoConnection sends an event to a single user.
func (h *Hub) WriteIntoConnection(clientID uuid.UUID, payload Event) error {
	return h.WriteToUsers([]uuid.UUID{clientID}, payload)
}

// WriteToUsers sends an event to every connected user in targets.
func (h *Hub) WriteToUsers(targets []uuid.UUID, payload Event) error {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	h.send(targets, bytes)
	return nil
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			h.mu.Lock()
			if old, ok := h.clients[client.UserID]; ok && old != client {
				// one connection per user: the newest one wins
				close(old.Send)
			}
			h.clients[client.UserID] = client
			h.mu.Unlock()

		case client := <-h.Unregister:
			h.remove(client)

		case event := <-h.Broadcast:
			var out Event
			var target string
			switch p := event.Payload.(type) {
			case TypoEvent:
				out = Event{Event: event.Event, Payload: OutGoingEventForTyping{FromID: p.FromID}}
				target = p.ToID
			case SeenEvent:
				out = Event{Event: event.Event, Payload: OutGoingEventForSeen{FromID: p.FromID, MsgID: p.MsgID}}
				target = p.ToID
			default:
				continue
			}
			toID, err := uuid.Parse(target)
			if err != nil {
				continue
			}
			if err := h.WriteIntoConnection(toID, out); err != nil {
				log.Printf("hub: failed to marshal event: %v", err)
			}
		}
	}
}

// remove drops client if it is still the registered connection for its user.
func (h *Hub) remove(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.clients[client.UserID]; ok && cur == client {
		delete(h.clients, client.UserID)
		close(client.Send)
	}
}

func (h *Hub) send(targets []uuid.UUID, bytes []byte) {
	var slow []*Client
	h.mu.RLock()
	for _, id := range targets {
		client, ok := h.clients[id]
		if !ok {
			continue
		}
		select {
		case client.Send <- bytes:
		default:
			slow = append(slow, client)
		}
	}
	h.mu.RUnlock()
	// clients whose buffers are full are disconnected
	for _, c := range slow {
		h.remove(c)
	}
}

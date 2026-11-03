package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

// Hub tracks live websocket clients and routes events to them.
// The clients map is guarded by mu; every method is safe for concurrent use.
type Hub struct {
	mu      sync.RWMutex
	clients map[uuid.UUID]*Client
	logger  *slog.Logger
	policy  EventPolicy

	Register   chan *Client
	Unregister chan *Client
}

// EventPolicy decides who may receive events that clients send over the
// socket. Anything it rejects is dropped, so strangers cannot push typing
// indicators or read receipts at arbitrary users.
type EventPolicy interface {
	// TypingTargets returns the recipients of from's typing indicator in a
	// private chat with user `to`, or (group=true) in group `to`.
	TypingTargets(ctx context.Context, from, to uuid.UUID, group bool) ([]uuid.UUID, error)
	// SeenAllowed reports whether from may send a read receipt to user `to`.
	SeenAllowed(ctx context.Context, from, to uuid.UUID) (bool, error)
}

// denyAll is used until SetPolicy is called.
type denyAll struct{}

func (denyAll) TypingTargets(context.Context, uuid.UUID, uuid.UUID, bool) ([]uuid.UUID, error) {
	return nil, nil
}
func (denyAll) SeenAllowed(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return false, nil }

// SetPolicy installs the event policy; call it before Run.
func (h *Hub) SetPolicy(p EventPolicy) { h.policy = p }

var ErrNoClientFound = errors.New("no client exist")

func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[uuid.UUID]*Client),
		logger:     logger,
		policy:     denyAll{},
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
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

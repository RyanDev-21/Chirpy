package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

// MaxDevices is how many live connections one user may have (phone,
// tablet, ...). Connecting another closes their oldest.
const MaxDevices = 5

// Hub tracks live websocket clients and routes events to them; a user may
// be connected from several devices and every device gets every event.
// The clients map is guarded by mu; every method is safe for concurrent use.
type Hub struct {
	mu      sync.RWMutex
	clients map[uuid.UUID][]*Client // oldest first
	logger  *slog.Logger
	policy  EventPolicy
	// presence, if set, is told when a user's first device connects
	// (online) and their last one goes (offline). It runs on its own
	// goroutine, outside the lock.
	presence func(userID uuid.UUID, online bool)

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

// OnPresence installs the online/offline hook; call it before Run.
func (h *Hub) OnPresence(fn func(userID uuid.UUID, online bool)) { h.presence = fn }

func (h *Hub) changed(userID uuid.UUID, online bool) {
	if h.presence != nil {
		go h.presence(userID, online)
	}
}

var ErrNoClientFound = errors.New("no client exist")

func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[uuid.UUID][]*Client),
		logger:     logger,
		policy:     denyAll{},
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
	}
}

func (h *Hub) CheckWsConnection(clientID uuid.UUID) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[clientID]) > 0
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
			first := len(h.clients[client.UserID]) == 0
			list := append(h.clients[client.UserID], client)
			if len(list) > MaxDevices {
				// too many devices: the oldest connection goes
				close(list[0].Send)
				list = list[1:]
			}
			h.clients[client.UserID] = list
			h.mu.Unlock()
			if first {
				h.changed(client.UserID, true)
			}

		case client := <-h.Unregister:
			h.remove(client)
		}
	}
}

// Count is how many users have at least one live connection to this server.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Online reports whether userID has a live connection on any device.
func (h *Hub) Online(userID uuid.UUID) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[userID]) > 0
}

// Disconnect closes all of userID's connections (account deleted, banned,
// signed out everywhere).
func (h *Hub) Disconnect(userID uuid.UUID) {
	h.mu.Lock()
	list := h.clients[userID]
	for _, c := range list {
		close(c.Send)
	}
	delete(h.clients, userID)
	h.mu.Unlock()
	if len(list) > 0 {
		h.changed(userID, false)
	}
}

// remove drops one connection if it is still registered (closing its
// Send exactly once).
func (h *Hub) remove(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.clients[client.UserID]
	for i, c := range list {
		if c == client {
			close(c.Send)
			list = append(list[:i:i], list[i+1:]...)
			if len(list) == 0 {
				delete(h.clients, client.UserID)
				h.changed(client.UserID, false) // async: safe under the lock
			} else {
				h.clients[client.UserID] = list
			}
			return
		}
	}
}

func (h *Hub) send(targets []uuid.UUID, bytes []byte) {
	var slow []*Client
	h.mu.RLock()
	for _, id := range targets {
		for _, client := range h.clients[id] {
			select {
			case client.Send <- bytes:
			default:
				slow = append(slow, client)
			}
		}
	}
	h.mu.RUnlock()
	// clients whose buffers are full are disconnected
	for _, c := range slow {
		h.remove(c)
	}
}

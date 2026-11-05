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
	// calls, if set, handles call signalling and hears about each
	// connection coming and going.
	calls CallHandler

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

// Conn identifies one live connection: a user on one device.
type Conn struct {
	User uuid.UUID
	ID   uuid.UUID
}

// CallHandler is implemented by the calls package. Connected and
// Disconnected run on their own goroutine.
type CallHandler interface {
	HandleCall(ctx context.Context, from Conn, event string, payload json.RawMessage)
	Connected(c Conn)
	Disconnected(c Conn)
}

// SetCalls installs the call handler; call it before Run.
func (h *Hub) SetCalls(c CallHandler) { h.calls = c }

// SendToConn sends an event to one connection; false if it is gone.
func (h *Hub) SendToConn(c Conn, payload Event) bool {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	h.mu.RLock()
	var target *Client
	for _, cl := range h.clients[c.User] {
		if cl.ID == c.ID {
			target = cl
		}
	}
	sent := false
	if target != nil {
		select {
		case target.Send <- bytes:
			sent = true
		default:
		}
	}
	h.mu.RUnlock()
	if target != nil && !sent {
		h.remove(target) // buffer full: drop it, like send does
	}
	return sent
}

// WriteToUserExcept sends an event to every connection of user but one.
func (h *Hub) WriteToUserExcept(user, except uuid.UUID, payload Event) {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.mu.RLock()
	for _, cl := range h.clients[user] {
		if cl.ID != except {
			select {
			case cl.Send <- bytes:
			default:
			}
		}
	}
	h.mu.RUnlock()
}

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
				if h.calls != nil {
					go h.calls.Disconnected(Conn{User: list[0].UserID, ID: list[0].ID})
				}
				list = list[1:]
			}
			h.clients[client.UserID] = list
			h.mu.Unlock()
			if first {
				h.changed(client.UserID, true)
			}
			if h.calls != nil {
				go h.calls.Connected(Conn{User: client.UserID, ID: client.ID})
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
		if h.calls != nil {
			go h.calls.Disconnected(Conn{User: userID, ID: c.ID})
		}
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
			if h.calls != nil {
				go h.calls.Disconnected(Conn{User: c.UserID, ID: c.ID})
			}
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

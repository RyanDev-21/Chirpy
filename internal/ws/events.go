package ws

import (
	"encoding/json"
	"errors"
	"time"
)

var ErrNotConnectedToWs = errors.New("not connected to ws")

// Event is the envelope for every message written to a websocket client.
type Event struct {
	Event   string `json:"event"`
	Payload any    `json:"payload"`
}

// event names sent to clients
const (
	EventMessage   = "msg"
	EventTyping    = "Typing"
	EventSeen      = "Seen"
	EventAddFriend = "AddFri"
	EventAccept    = "AcceptFri"
	EventDeny      = "DenyFri"
)

// InCommingEvent is the envelope clients send over the socket.
type InCommingEvent struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

type InCommingEventForSeen struct {
	ToID  string `json:"to_id,omitempty"`
	MsgID string `json:"msg_id"`
}

// InCommingEventForTyping: to_id is a user (type "private", the default)
// or a group (type "public").
type InCommingEventForTyping struct {
	ToID string `json:"to_id"`
	Type string `json:"type,omitempty"`
}

type OutGoingEventForSeen struct {
	MsgID  string `json:"msg_id"`
	FromID string `json:"from_id"`
}

type OutGoingEventForTyping struct {
	FromID  string `json:"from_id"`
	GroupID string `json:"group_id,omitempty"`
}

type OutFriEvent struct {
	ReqID  string `json:"req_id"`
	FromID string `json:"from_id"`
}

type OutGoingMessage struct {
	ID       string `json:"id"`
	Content  string `json:"msg"`
	ParentID string `json:"parent_id,omitempty"`
	FromID   string `json:"from_id"`
	Type     string `json:"type"`
	// GroupID is set for group ("public") messages so the client knows
	// which group chat the message belongs to.
	GroupID   string    `json:"group_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

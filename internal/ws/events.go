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
	EventMessage    = "msg"
	EventTyping     = "Typing"
	EventSeen       = "Seen"
	EventAddFriend  = "AddFri"
	EventAccept     = "AcceptFri"
	EventDeny       = "DenyFri"
	EventUnfriend   = "RemoveFri"
	EventReaction   = "Reaction"
	EventEdited     = "MsgEdited"
	EventDeleted    = "MsgDeleted"
	EventGroupUpd   = "GroupUpdated"
	EventGroupGone  = "GroupRemoved"
	EventGroupRead  = "GroupRead"
	EventGroupAdded = "GroupAdded"
	EventPresence   = "Presence"
)

// OutGroupAdded tells a user that by_id (the leader) added them to group_id.
type OutGroupAdded struct {
	GroupID string `json:"group_id"`
	ByID    string `json:"by_id"`
}

// OutGroupReadEvent: user_id has read group_id up to msg_id (groups of up
// to 50 members only).
type OutGroupReadEvent struct {
	GroupID string `json:"group_id"`
	UserID  string `json:"user_id"`
	MsgID   string `json:"msg_id"`
}

// OutPresenceEvent: a friend came online or went offline. Hidden means
// they (or you) hide last seen; online and last_seen_at are then absent.
type OutPresenceEvent struct {
	UserID     string     `json:"user_id"`
	Online     bool       `json:"online"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	Hidden     bool       `json:"hidden,omitempty"`
}

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

// OutReactionEvent: from_id set (or, with an empty emoji, removed) their
// reaction on msg_id. group_id is present for group messages.
type OutReactionEvent struct {
	MsgID   string `json:"msg_id"`
	FromID  string `json:"from_id"`
	Type    string `json:"type"`
	GroupID string `json:"group_id,omitempty"`
	Emoji   string `json:"emoji"`
}

// OutEditedEvent: from_id edited msg_id; msg is the new text.
type OutEditedEvent struct {
	MsgID    string    `json:"msg_id"`
	FromID   string    `json:"from_id"`
	Type     string    `json:"type"`
	GroupID  string    `json:"group_id,omitempty"`
	Content  string    `json:"msg"`
	EditedAt time.Time `json:"edited_at"`
}

// OutDeletedEvent: from_id deleted msg_id for everyone.
type OutDeletedEvent struct {
	MsgID   string `json:"msg_id"`
	FromID  string `json:"from_id"`
	Type    string `json:"type"`
	GroupID string `json:"group_id,omitempty"`
}

// OutGroupUpdated tells members that a group changed; change is "info"
// (name/description/picture), "leader" (user_id is the new leader),
// "members" (user_id was removed) or "joined" (the leader added user_id). Clients refetch the group for details.
type OutGroupUpdated struct {
	GroupID string `json:"group_id"`
	Change  string `json:"change"`
	UserID  string `json:"user_id,omitempty"`
}

// OutGroupRemoved tells a user they are no longer in a group: reason is
// "kicked" or "deleted".
type OutGroupRemoved struct {
	GroupID string `json:"group_id"`
	Reason  string `json:"reason"`
}

// OutUnfriendEvent tells a user that from_id removed them as a friend.
type OutUnfriendEvent struct {
	FromID string `json:"from_id"`
}

type OutGoingMessage struct {
	ID       string `json:"id"`
	Content  string `json:"msg"`
	ParentID string `json:"parent_id,omitempty"`
	FromID   string `json:"from_id"`
	// ToID is the recipient of a private message, so the sender's other
	// devices know which chat it belongs to.
	ToID string `json:"to_id,omitempty"`
	Type string `json:"type"`
	// GroupID is set for group ("public") messages so the client knows
	// which group chat the message belongs to.
	GroupID   string    `json:"group_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Media: the message's attachments (see chat history), if any.
	Media any `json:"media,omitempty"`
}

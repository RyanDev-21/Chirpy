package chat

import (
	"errors"
	"fmt"
	"time"

	"RyanDev-21.com/Chirpy/internal/media"
	"github.com/google/uuid"
)

const (
	TypePrivate = "private"
	TypePublic  = "public"
)

// TopicSeen is the only chat write that goes through the queue: receipts
// are frequent, only the latest matters, and losing one is harmless.
// Messages are written to postgres before the API responds.
const TopicSeen = "chat.seen.update"

var (
	ErrNotAuthorized       = errors.New("not a member of this group")
	ErrNotFriends          = errors.New("you can only message friends")
	ErrNotValidToID        = errors.New("to_id must be a valid uuid of another user or group")
	ErrNotValidUUID        = errors.New("not valid uuid")
	ErrNotValidTimeFmt     = errors.New("since must be an RFC3339 timestamp")
	ErrNotValidParentID    = errors.New("parent_id must be the id of a message in the same chat")
	ErrNotSupportedTypeMsg = errors.New(`type must be "private" or "public"`)
	ErrEmptyMessage        = errors.New("msg must be 1-500 characters")
	ErrMessageNotInChat    = errors.New("msg_id is not a message in this chat")
	ErrBadCursor           = errors.New("before must be the id of a message in this chat")
	ErrSinceAndBefore      = errors.New("use either since or before, not both")
	ErrMessageNotFound     = errors.New("message not found")
	ErrBadClientID         = errors.New("client_msg_id must be a UUID")
	ErrNotYourMessage      = errors.New("you can only change your own messages")
	ErrEditWindowClosed    = errors.New("messages can only be edited within 15 minutes of sending")
	ErrNotGroupMessage     = errors.New("read-by is only for group messages")
	ErrGroupTooBig         = errors.New("read-by is only shown in groups of up to 50 members")
)

// EditWindow is how long after sending the sender may edit a message.
// Deleting (for everyone) has no time limit.
const EditWindow = 15 * time.Minute

const maxMessageLen = 500 // matches message.content VARCHAR(500)

// InCommingMessage is the body of POST /api/chats/message.
type InCommingMessage struct {
	Content  string `json:"msg"`
	ParendID string `json:"parent_id,omitempty"`
	ToID     string `json:"to_id"`
	Type     string `json:"type"`
	// Media lists uploaded media ids (POST /api/media) to attach; a
	// message with media may have no text.
	Media []uuid.UUID `json:"media,omitempty"`
	// ClientMsgID (optional, a UUID the app makes per message) makes
	// retries safe: the same id within DedupeWindow returns the first
	// message instead of sending it twice.
	ClientMsgID string `json:"client_msg_id,omitempty"`
	// Forwarded is set by the server when forwarding (never by clients).
	Forwarded bool `json:"-"`
}

type PayloadForSeen struct {
	MsgID string `json:"msg_id"`
}

// MessageCache is the stored message body. Field names are part of the
// public API (message_info) and stay as they were.
type MessageCache struct {
	Msg       InCommingMessage
	FromID    uuid.UUID
	CreatedAt time.Time
}

type MessageMetaDataRes struct {
	ID        uuid.UUID    `json:"message_id"`
	MsgInfo   MessageCache `json:"message_info"`
	CreatedAt time.Time    `json:"created_at"`
	// EditedAt is set once the sender edits the message.
	EditedAt *time.Time `json:"edited_at"`
	// ChangedAt is the last edit, delete or reaction change (omitted if none).
	ChangedAt *time.Time `json:"changed_at,omitempty"`
	// ClientMsgID is the id the app sent the message with: only shown to
	// its sender, so the app can match it to a message still waiting.
	ClientMsgID *uuid.UUID `json:"client_msg_id,omitempty"`
	// Forwarded: shown as "Forwarded"; who wrote it first isn't revealed.
	Forwarded bool `json:"forwarded"`
	// Deleted messages stay in history as placeholders (content "", no
	// reactions) so replies to them still make sense.
	Deleted bool `json:"deleted"`
	// Reactions are filled in when history is read (never cached with the
	// message, since they change afterwards).
	Reactions []ReactionCount `json:"reactions"`
	// Media is the message's photos and videos (empty list if none).
	Media []media.Attachment `json:"media"`
	// SenderSuspended: the sender is banned; text and media are hidden
	// (kept on the server, shown again if the ban is lifted).
	SenderSuspended bool `json:"sender_suspended"`
}

// Change is a message in one of my chats that was edited, deleted or
// reacted to: chat_id is the other person (private) or the group.
type Change struct {
	Type    string             `json:"type"`
	ChatID  string             `json:"chat_id"`
	Message MessageMetaDataRes `json:"message"`
}

// ChangesRes is GET /api/chats/changes. With more, ask again with since =
// the last change's message.changed_at.
type ChangesRes struct {
	Changes []Change `json:"changes"`
	More    bool     `json:"more"`
}

// ReactionCount is one emoji on a message: how many people used it and
// whether the caller is one of them.
type ReactionCount struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Me    bool   `json:"me"`
}

// Reactor is one person's reaction, for GET .../reactions.
type Reactor struct {
	UserID      uuid.UUID `json:"user_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Emoji       string    `json:"emoji"`
	CreatedAt   time.Time `json:"created_at"`
}

// Reader is a group member who has read a message (see ReadBy).
type Reader struct {
	UserID      uuid.UUID `json:"user_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	AvatarURL   *string   `json:"avatar_url"`
	ReadAt      time.Time `json:"read_at"`
}

// ReadByMaxMembers: "read by" is only kept for groups up to this size, as
// in other chat apps; bigger groups show no per-member receipts.
const ReadByMaxMembers = 50

// messageRef locates a message: a private one (between From and To) or a
// group one (in Group). From is always the sender.
type messageRef struct {
	Kind      string // TypePrivate or TypePublic
	From, To  uuid.UUID
	Group     uuid.UUID
	CreatedAt time.Time
}

// EditedMessage is the response to an edit.
type EditedMessage struct {
	MsgID    uuid.UUID `json:"msg_id"`
	Content  string    `json:"msg"`
	EditedAt time.Time `json:"edited_at"`
}

type MessageListRes struct {
	ChatID  string               `json:"chat_id"`
	MsgList []MessageMetaDataRes `json:"msgList"`
	// PeerSeenID (private chats, friends only): the newest message the other
	// person has read, so ticks survive reopening the chat. Omitted if none.
	PeerSeenID *uuid.UUID `json:"peer_seen_id,omitempty"`
}

type ResponseMessageID struct {
	MsgID uuid.UUID `json:"msg_id"`
}

// Conversation is one row of the chat list (GET /api/chats).
type Conversation struct {
	Type        string       `json:"type"`         // "private" or "public"
	ID          uuid.UUID    `json:"id"`           // the other user, or the group
	Name        string       `json:"name"`         // the other user's username, or the group name
	DisplayName string       `json:"display_name"` // what to show: display name, or the group name
	AvatarURL   *string      `json:"avatar_url"`   // the other user's or the group's picture
	LastMessage *LastMessage `json:"last_message"` // null for a group with no messages
	Unread      int          `json:"unread"`
}

type LastMessage struct {
	ID     uuid.UUID `json:"id"`
	Msg    string    `json:"msg"`
	FromID uuid.UUID `json:"from_id"` // zero uuid if the sender deleted their account
	// FromName (groups only): the sender's display name, for "Jon: hi".
	FromName  string    `json:"from_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Deleted   bool      `json:"deleted"` // msg is "" then
	// SenderSuspended: the sender is banned, msg is "" then.
	SenderSuspended bool `json:"sender_suspended"`
}

// JobSeen is the payload of TopicSeen.
type JobSeen struct {
	ChatID string    `json:"chat_id"`
	MsgID  uuid.UUID `json:"msg_id"`
	SeenID uuid.UUID `json:"seen_id"`
}

// PrivateChatID is the stable id for the conversation between two users.
func PrivateChatID(a, b uuid.UUID) string {
	if a.String() > b.String() {
		a, b = b, a
	}
	return fmt.Sprintf("%v_%v", a, b)
}

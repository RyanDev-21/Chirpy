package chat

import (
	"errors"
	"fmt"
	"time"

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
	ErrSinceNotSupported   = errors.New("since is not supported for group history")
	ErrMessageNotFound     = errors.New("message not found")
	ErrNotYourMessage      = errors.New("you can only change your own messages")
	ErrEditWindowClosed    = errors.New("messages can only be edited within 15 minutes of sending")
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
	// Deleted messages stay in history as placeholders (content "", no
	// reactions) so replies to them still make sense.
	Deleted bool `json:"deleted"`
	// Reactions are filled in when history is read (never cached with the
	// message, since they change afterwards).
	Reactions []ReactionCount `json:"reactions"`
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
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name"`
	Emoji     string    `json:"emoji"`
	CreatedAt time.Time `json:"created_at"`
}

// messageRef locates a message: a private one (between From and To) or a
// group one (in Group). From is always the sender.
type messageRef struct {
	Kind      string // TypePrivate or TypePublic
	From, To  uuid.UUID
	Group     uuid.UUID
	CreatedAt time.Time
}

// messageChange is the edit/delete state of a message, read from postgres
// and laid over cached copies (which are written once, at send time).
type messageChange struct {
	Content  string
	EditedAt *time.Time
	Deleted  bool
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
}

type ResponseMessageID struct {
	MsgID uuid.UUID `json:"msg_id"`
}

// Conversation is one row of the chat list (GET /api/chats).
type Conversation struct {
	Type        string       `json:"type"` // "private" or "public"
	ID          uuid.UUID    `json:"id"`   // the other user, or the group
	Name        string       `json:"name"`
	LastMessage *LastMessage `json:"last_message"` // null for a group with no messages
	Unread      int          `json:"unread"`
}

type LastMessage struct {
	ID        uuid.UUID `json:"id"`
	Msg       string    `json:"msg"`
	FromID    uuid.UUID `json:"from_id"` // zero uuid if the sender deleted their account
	CreatedAt time.Time `json:"created_at"`
	Deleted   bool      `json:"deleted"` // msg is "" then
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

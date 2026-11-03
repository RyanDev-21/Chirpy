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

// queue topics owned by the chat package
const (
	TopicPrivateMessage = "chat.message.private"
	TopicPublicMessage  = "chat.message.public"
	TopicSeen           = "chat.seen.update"
)

var (
	ErrNotAuthorized       = errors.New("not a member of this group")
	ErrNotFriends          = errors.New("you can only message friends")
	ErrNotValidToID        = errors.New("to_id must be a valid uuid of another user or group")
	ErrNotValidUUID        = errors.New("not valid uuid")
	ErrNotValidTimeFmt     = errors.New("since must be an RFC3339 timestamp")
	ErrNotValidParentID    = errors.New("parent_id must be a valid uuid")
	ErrNotSupportedTypeMsg = errors.New(`type must be "private" or "public"`)
	ErrEmptyMessage        = errors.New("msg must be 1-500 characters")
)

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
}

type MessageListRes struct {
	ChatID  string               `json:"chat_id"`
	MsgList []MessageMetaDataRes `json:"msgList"`
}

type ResponseMessageID struct {
	MsgID uuid.UUID `json:"msg_id"`
}

// JobMessage is the payload of the message topics.
type JobMessage struct {
	Message MessageMetaDataRes `json:"message"`
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

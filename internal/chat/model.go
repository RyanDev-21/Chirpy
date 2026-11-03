package chat

import (
	"errors"
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
	ErrNotAuthorized       = errors.New("not in group")
	ErrNotValidToID        = errors.New("not valid toID(type uuid)")
	ErrNotValidUUID        = errors.New("not valid uuid")
	ErrNotValidTimeFmt     = errors.New("not valid time")
	ErrNotValidParentID    = errors.New("not valid parentID(type uuid)")
	ErrNotSupportedTypeMsg = errors.New("not supported type of message")
	ErrEmptyMessage        = errors.New("message content is empty or too long")
)

const maxMessageLen = 500 // matches message.content VARCHAR(500)

type InCommingMessage struct {
	Content  string `json:"msg"`
	ParendID string `json:"parent_id,omitempty"`
	ToID     string `json:"to_id"`
	Type     string `json:"type"`
}

type PayloadForSeen struct {
	MsgID string `json:"msg_id"`
}

type MessageCache struct {
	Msg       InCommingMessage
	FromID    uuid.UUID
	CreatedAt time.Time
}

type MessageMetaData struct {
	ID      uuid.UUID
	MsgInfo *MessageCache
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

type JobForSeen struct {
	ChatID string
	MsgID  uuid.UUID
	SeenID uuid.UUID
}

package chatmodel

import (
	// "context"
	// "fmt"
	// "time"

	"errors"

	"github.com/google/uuid"
)

type Message struct {
	Content  string `json:"msg"`
	ParendID string `json:"parent_id,omitempty"`
	ToID     string `json:"to_id"`
	Type     string `json:"type"`
}
type PublishMessageStruct struct {
	Msg    *Message
	UserID uuid.UUID
}

type GroupActionInfo struct {
	UserID  uuid.UUID
	GroupID uuid.UUID
}
type MessageCache struct {
	Msg    Message
	FromID uuid.UUID
}
type MessageList struct {
	MsgList *[]MessageMetaData
}

type MessageListRes struct {
	ChatID  string               `json:"chat_id"`
	MsgList []MessageMetaDataRes `json:"msgList"`
}
type MessageMetaData struct {
	ID      uuid.UUID
	MsgInfo *MessageCache
}

type MessageMetaDataRes struct {
	ID      uuid.UUID    `json:"message_id"`
	MsgInfo MessageCache `json:"message_info"`
}

type ResponseMessageID struct {
	MsgID uuid.UUID `json:"msg_id"`
}

// var (
//
//	ErrUpdateCache         = errors.New("failed to update the cache")
//	ErrRedisCacheSetFailed = errors.New("failed to set the redis cache")
//
// )
var (
	ErrNotAuthorized = errors.New("not in group")
	ErrNotValidToID  = errors.New("not valid toID(type uuid)")
)

const (
	PrivateMessageConstant = "addPrivateMessage"
	PublicMessageConstant  = "addPublicMessage"
)

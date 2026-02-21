package chatmodel

import (
	// "context"
	// "fmt"
	// "time"

	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

type InCommingMessage struct {
	Content  string `json:"msg"`
	ParendID string `json:"parent_id,omitempty"`
	ToID     string `json:"to_id"`
	Type     string `json:"type"`
}
type Event struct {
	Event   string `json:"event"`
	Payload any    `json:"payload"`
}

type InCommingEvent struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

// type Event struct {
// 	FromID   string `json:"from_id"`
// 	ToID     string `json:"to_id"`
// 	ParentID string `json:"parent_id,omitempty"`
// 	Content  string `json:"content,omitempty"`
// 	Type     string `json:"type,omitempty"`
// }

type OutFriEvent struct {
	ReqID  string `json:"req_id"`
	FromID string `json:"from_id"`
}

type OutGoingEventForTyping struct {
	FromID string `json:"from_id"`
}
type TypoEvent struct {
	FromID string
	ToID   string
}

type InCommingEventForTyping struct {
	ToID string `json:"to_id"`
}

//	type Event struct {
//		FromID string `json:"from_id"`
//		ToID   string `json:"to_id"`
//		Event  string `json:"event"`
//	}
type OutGoingMessage struct {
	Content  string `json:"msg"`
	ParentID string `json:"parent_id,omitempty"`
	FromID   string `json:"from_id"`
	Type     string `json:"type"`
}

type Message struct {
	Content  string
	ParentID string
	FromID   string
	ToID     string
	Type     string
}
type PublishMessageStruct struct {
	Msg    *InCommingMessage
	UserID uuid.UUID
}

type GroupActionInfo struct {
	UserID  uuid.UUID
	GroupID uuid.UUID
}
type MessageCache struct {
	Msg    InCommingMessage
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
	ErrNotAuthorized    = errors.New("not in group")
	ErrNotValidToID     = errors.New("not valid toID(type uuid)")
	ErrNotConnectedToWs = errors.New("not connected to ws")
)

const (
	PrivateMessageConstant = "addPrivateMessage"
	PublicMessageConstant  = "addPublicMessage"
)

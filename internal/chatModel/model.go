package chatmodel

import (
	// "context"
	// "fmt"
	// "time"

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

type MessageMetaData struct {
	ID      uuid.UUID
	MsgInfo *MessageCache
}

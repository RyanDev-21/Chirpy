package groups

import (
	"errors"

	"github.com/google/uuid"
)


var ErrNotFoundGroup = errors.New("group not found")
var ErrGroupFull = errors.New("group full")

type createGroupRequest struct{
	GroupName string  `json:"group_name"`
	Members []uuid.UUID `json:"member_ids"`
	Description string `json:"description"`
	MaxMems int16 `json:"max_mems"`
}

type GroupInfo struct{
	ChatID uuid.UUID `json:"chat_id"`
}

type GroupPublish struct{
	GroupID GroupInfo `json:"chat_id"`	
	GroupInfo createGroupRequest `json:"group_info"`	
}

type creatorPublishStruct struct{
	GroupID uuid.UUID
	UserID uuid.UUID
	Role string
}

type ManageGroupPublishStruct struct{
	GroupId uuid.UUID
	UserID uuid.UUID
}



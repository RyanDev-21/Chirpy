package groups

import (
	"errors"

	"github.com/google/uuid"
)


var ErrNotFoundGroup = errors.New("group not found")
var ErrGroupFull = errors.New("group full")

type createGroupRequest struct{
	Members []uuid.UUID `json:"member_ids"`
	GroupName string  `json:"group_name"`
	Description string `json:"description"`
	MaxMems int16 `json:"max_mems"`
}

type createGroupStruct struct{
	GroupID uuid.UUID
	GroupName string
	Description string
	MaxMems int16
	CurrentMem int16	
	Members []uuid.UUID
}

type GroupInfo struct{
	ChatID uuid.UUID `json:"chat_id"`
}

type GroupPublish struct{
	GroupID GroupInfo 	
	CreatorID uuid.UUID
	GroupInfo createGroupStruct 	
	Role string
}

type creatorPublishStruct struct{
	Role string
	GroupID uuid.UUID
	UserID uuid.UUID
}

type ManageGroupPublishStruct struct{
	GroupId uuid.UUID
	UserID uuid.UUID
}

type membersPubStruct struct{
	UserIds []uuid.UUID
	GroupId uuid.UUID	
}





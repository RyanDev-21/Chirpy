package groups

import "github.com/google/uuid"


type createGroupRequest struct{
	GroupName string  `json:"group_name"`
	Members []uuid.UUID `json:"member_ids"`	
	MaxMems uint16 `json:"max_mems"`
}

type GroupInfo struct{
	ChatID uuid.UUID `json:"chat_id"`
}

type GroupPublish struct{
	GroupID GroupInfo `json:"chat_id"`	
	GroupInfo createGroupRequest `json:"group_info"`	
}





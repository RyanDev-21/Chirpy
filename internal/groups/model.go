package groups

import (
	"errors"

	"github.com/google/uuid"
)

var (
	ErrNotFoundGroup   = errors.New("group not found")
	ErrGroupFull       = errors.New("group full")
	ErrAlreadyMember   = errors.New("already a member")
	ErrNotMember       = errors.New("not a member")
	ErrDuplicateName   = errors.New("duplicate name")
	ErrInvalidGroupReq = errors.New("invalid group request")
)

// queue topics owned by the groups package
const (
	TopicCreateGroup  = "group.create"
	TopicAddMembers   = "group.members.add_list"
	TopicAddMember    = "group.member.add"
	TopicRemoveMember = "group.member.remove"
)

const (
	maxGroupNameLen = 30
	maxDescLen      = 1000
	maxGroupSize    = 1000
)

type createGroupRequest struct {
	GroupName   string `json:"group_name"`
	Description string `json:"description"`
	MaxMems     int16  `json:"max_mems"`
}

type createGroupStruct struct {
	GroupID     uuid.UUID
	GroupName   string
	Description string
	MaxMems     int16
	CurrentMem  int16
}

type GroupInfo struct {
	ChatID uuid.UUID `json:"chat_id"`
}

type GroupPublish struct {
	GroupID   GroupInfo
	CreatorID uuid.UUID
	GroupInfo createGroupStruct
	Role      string
}

type creatorPublishStruct struct {
	Role    string
	GroupID uuid.UUID
	UserID  uuid.UUID
}

type ManageGroupPublishStruct struct {
	GroupId uuid.UUID
	UserID  uuid.UUID
}

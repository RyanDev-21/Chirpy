package groups

import (
	"errors"

	"github.com/google/uuid"
)

var (
	ErrNotFoundGroup     = errors.New("group not found")
	ErrGroupFull         = errors.New("group full")
	ErrAlreadyMember     = errors.New("already a member")
	ErrNotMember         = errors.New("not a member")
	ErrDuplicateName     = errors.New("duplicate name")
	ErrInvalidGroupReq   = errors.New("group_name must be 1-30 characters, description at most 1000, max_mems 2-1000")
	ErrLeaderCannotLeave = errors.New("group leader cannot leave the group")
)

const RoleLeader = "Leader"

// queue topics owned by the groups package
const (
	TopicCreateGroup  = "group.create"
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

type GroupInfo struct {
	ChatID uuid.UUID `json:"chat_id"`
}

// JobCreate is the payload of TopicCreateGroup.
type JobCreate struct {
	GroupID     uuid.UUID `json:"group_id"`
	CreatorID   uuid.UUID `json:"creator_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	MaxMembers  int16     `json:"max_members"`
}

// JobMember is the payload of TopicAddMember and TopicRemoveMember.
type JobMember struct {
	GroupID uuid.UUID `json:"group_id"`
	UserID  uuid.UUID `json:"user_id"`
}

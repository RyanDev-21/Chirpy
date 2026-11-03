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

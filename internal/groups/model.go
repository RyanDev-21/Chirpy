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
	ErrNotLeader         = errors.New("only the group leader can do this")
	ErrKickSelf          = errors.New("the leader can't remove themselves; transfer leadership or delete the group")
	ErrAlreadyLeader     = errors.New("that member is already the leader")
	ErrNothingToUpdate   = errors.New("send group_name and/or description")
	ErrInvalidGroupInfo  = errors.New("group_name must be 1-30 characters, description at most 1000")
)

// RoleMember is the role of everyone but the leader.
const RoleMember = "member"

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

// updateGroupRequest is PATCH /api/chats/groups/{id}; absent fields stay.
type updateGroupRequest struct {
	GroupName   *string `json:"group_name"`
	Description *string `json:"description"`
}

type GroupInfo struct {
	ChatID uuid.UUID `json:"chat_id"`
}

// GroupPreview is what an invite code reveals before joining.
type GroupPreview struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	MaxMembers  int16     `json:"max_members"`
	MemberCount int16     `json:"member_count"`
}

type InviteCode struct {
	Code string `json:"code"` // display form, e.g. K7M9-QX2P
}

// GroupSummary is one entry of GET /api/chats/groups.
type GroupSummary struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	MaxMembers  int16     `json:"max_members"`
	Members     int16     `json:"member_count"`
	Role        string    `json:"role"` // the caller's role: "Leader" or "member"
}

type GroupMember struct {
	UserID      uuid.UUID `json:"user_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	AvatarURL   *string   `json:"avatar_url"`
}

// GroupDetail is GET /api/chats/groups/{group_id}. Members is only filled
// in for members of the group.
type GroupDetail struct {
	ID          uuid.UUID     `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	MaxMembers  int16         `json:"max_members"`
	MemberCount int16         `json:"member_count"`
	IsMember    bool          `json:"is_member"`
	Members     []GroupMember `json:"members,omitempty"`
}

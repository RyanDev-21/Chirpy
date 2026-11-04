package groups

import (
	"errors"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrNotFoundGroup    = errors.New("group not found")
	ErrGroupFull        = errors.New("group full")
	ErrAlreadyMember    = errors.New("already a member")
	ErrNotMember        = errors.New("not a member")
	ErrInvalidGroupReq  = errors.New(`group_name must be 1-30 characters, description at most 1000, max_mems 2-1000, visibility "private" or "public"`)
	ErrNoPermission     = errors.New("you don't have permission to do this")
	ErrKickSelf         = errors.New("you can't remove yourself; leave the group instead")
	ErrCantRemoveAdmin  = errors.New("the group admin can't be removed")
	ErrAlreadyAdmin     = errors.New("that member is already the admin")
	ErrNothingToUpdate  = errors.New("send group_name, description and/or visibility")
	ErrInvalidGroupInfo = errors.New(`group_name must be 1-30 characters, description at most 1000, visibility "private" or "public"`)
	ErrNotFriend        = errors.New("you can only add your friends")
	ErrNoAvatar         = errors.New("no group picture")
	ErrBadRole          = errors.New(`role must be "moderator" or "member"; permissions from add_members, remove_members, delete_messages, edit_info, manage_invite`)
	ErrSelfRole         = errors.New("you can't change your own role")
	ErrBadSearch        = errors.New("q must be 2-30 characters")
)

// Roles. The admin runs the group (one per group: its creator, or whoever
// they handed it to). Moderators have the permissions the admin picked.
const (
	RoleAdmin     = "admin"
	RoleModerator = "moderator"
	RoleMember    = "member"
)

// Moderator permissions.
const (
	PermAddMembers     = "add_members"     // add their friends directly
	PermRemoveMembers  = "remove_members"  // remove ordinary members
	PermDeleteMessages = "delete_messages" // delete members' messages
	PermEditInfo       = "edit_info"       // name, description, picture
	PermManageInvite   = "manage_invite"   // replace the invite code
)

// AllPermissions lists every moderator permission.
var AllPermissions = []string{PermAddMembers, PermRemoveMembers, PermDeleteMessages, PermEditInfo, PermManageInvite}

// Visibility of a group.
const (
	Private = "private" // invite code or being added; invisible otherwise
	Public  = "public"  // found by search, anyone can join
)

const (
	maxGroupNameLen = 30
	maxDescLen      = 1000
	maxGroupSize    = 1000
)

// allowed reports whether someone with role and perms may do perm; "" is
// admin-only.
func allowed(role string, perms []string, perm string) bool {
	switch role {
	case RoleAdmin:
		return true
	case RoleModerator:
		return perm != "" && slices.Contains(perms, perm)
	}
	return false
}

// cleanPermissions validates and de-duplicates a permission list, in the
// order of AllPermissions.
func cleanPermissions(in []string) ([]string, error) {
	for _, p := range in {
		if !slices.Contains(AllPermissions, p) {
			return nil, ErrBadRole
		}
	}
	out := []string{}
	for _, p := range AllPermissions {
		if slices.Contains(in, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

type createGroupRequest struct {
	GroupName   string `json:"group_name"`
	Description string `json:"description"`
	MaxMems     int16  `json:"max_mems"`
	Visibility  string `json:"visibility"` // default private
}

// updateGroupRequest is PATCH /api/chats/groups/{id}; absent fields stay.
type updateGroupRequest struct {
	GroupName   *string `json:"group_name"`
	Description *string `json:"description"`
	Visibility  *string `json:"visibility"` // admin only
}

// roleRequest is PUT /api/chats/groups/{id}/members/{user_id}/role.
type roleRequest struct {
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

type GroupInfo struct {
	ChatID uuid.UUID `json:"chat_id"`
}

// GroupPreview is what an invite code or a search reveals before joining.
type GroupPreview struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	MaxMembers  int16     `json:"max_members"`
	MemberCount int16     `json:"member_count"`
	Visibility  string    `json:"visibility"`
	// AvatarURL: public groups only.
	AvatarURL *string `json:"avatar_url"`
	// IsMember: search results only.
	IsMember *bool `json:"is_member,omitempty"`
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
	Visibility  string    `json:"visibility"`
	Role        string    `json:"role"`        // the caller's role
	Permissions []string  `json:"permissions"` // the caller's, if a moderator
	AvatarURL   *string   `json:"avatar_url"`
}

type GroupMember struct {
	UserID      uuid.UUID `json:"user_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	Permissions []string  `json:"permissions"` // moderators only (else empty)
	AvatarURL   *string   `json:"avatar_url"`
}

// GroupDetail is GET /api/chats/groups/{group_id}. Members is only filled
// in for members of the group.
type GroupDetail struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	MaxMembers  int16     `json:"max_members"`
	MemberCount int16     `json:"member_count"`
	Visibility  string    `json:"visibility"`
	IsMember    bool      `json:"is_member"`
	// AvatarURL: members, or anyone for a public group.
	AvatarURL *string       `json:"avatar_url"`
	Members   []GroupMember `json:"members,omitempty"`
}

// AvatarURL is where a group's picture is served, versioned by upload time
// (nil: no picture).
func AvatarURL(id uuid.UUID, at pgtype.Timestamp) *string {
	if !at.Valid {
		return nil
	}
	u := "/api/chats/groups/" + id.String() + "/avatar?v=" + strconv.FormatInt(at.Time.UnixMilli(), 10)
	return &u
}

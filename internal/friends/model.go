// Package friends implements friend requests and friend lists.
package friends

import (
	"errors"

	"github.com/google/uuid"
)

var (
	ErrSelfRequest    = errors.New("cannot send a friend request to yourself")
	ErrRequestExists  = errors.New("a pending request already exists between these users")
	ErrAlreadyFriends = errors.New("already friends")
	ErrNotFound       = errors.New("friend request not found")
	ErrUnknownUser    = errors.New("user does not exist")
	ErrNotFriends     = errors.New("you are not friends with this user")
)

type FriendMetaData struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name"`
}

// Snapshot is everything cached about one user's relationships.
type Snapshot struct {
	Friends  []FriendMetaData
	Incoming map[uuid.UUID]FriendMetaData // reqID -> sender
	Outgoing map[uuid.UUID]FriendMetaData // reqID -> receiver
}

// HTTP shapes (kept compatible with the previous API)

type StatusFriendParameters struct {
	ToID   uuid.UUID `json:"to_id,omitempty"`
	Status string    `json:"status,omitempty"`
}

type ResponseReqList struct {
	PendingIDsList map[uuid.UUID]FriendMetaData `json:"pending_ids"`
	RequestIDsList map[uuid.UUID]FriendMetaData `json:"request_ids"`
}

type ResponseFriListStruct struct {
	FriendList []FriendMetaData `json:"id_list"`
}

type ResponseForAddFriend struct {
	ReqID uuid.UUID `json:"req_id"`
}

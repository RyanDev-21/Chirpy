package users

import (
	"time"

	"github.com/google/uuid"
)

type PasswordUpdateStruct struct {
	OldPass string `json:"old_password"`
	NewPass string `json:"new_password"`
}

const (
	DeleteFriReq     = "deleteFriReq"
	CancelReq        = "cancelReq"
	ConfirmFriendReq = "confirmFriReq"
	SendRequest      = "sendRequest"
)

type User struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email     string    `json:"email"`
	IsRED     bool      `json:"is_chirpy_red"`
}

type CreateUserInput struct {
	Name     string
	Email    string
	Password string
}

type UpdateUserPassword struct {
	UserID   uuid.UUID
	Password string
}

type DefaultUsersParameters struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// may be there will be better way than passing the to_id
type StatusFriendParameters struct {
	ToID   uuid.UUID `json:"to_id"`
	Status string    `json:"status"`
}

type CacheUpdateStruct struct {
	UserID      uuid.UUID
	ReqID       uuid.UUID
	OtherUserID uuid.UUID
	Lable       string
}

type CacheRsDeleteStruct struct {
	UserID uuid.UUID
	ReqID  uuid.UUID
	Lable  string
}

type FriendReq struct {
	ReqID  uuid.UUID
	FromID uuid.UUID
	ToID   uuid.UUID
}
type CancelFriendReq struct {
	ReqID      uuid.UUID
	UpdateTime time.Time
}

type CacheUpdateFriStruct struct {
	UserID uuid.UUID
	ToID   uuid.UUID
	Lable  string
}

type GetReqList struct {
	PendingIDsList *map[uuid.UUID]uuid.UUID
	RequestIDsList *map[uuid.UUID]uuid.UUID
}

type ResponseReqList struct {
	PendingIDsList map[uuid.UUID]uuid.UUID `json:"pending_ids"`
	RequestIDsList map[uuid.UUID]uuid.UUID `json:"request_ids"`
}

type ResponseFriListStruct struct {
	FriendList []uuid.UUID `json:"id_list"`
}

type DeleteFirReqStruct struct {
	ReqID uuid.UUID
}

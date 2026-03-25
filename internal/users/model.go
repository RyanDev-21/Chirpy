package users

import (
	//	"errors"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrReqExist    = errors.New("req already exist")
	ErrNoRedFound  = errors.New("no record found")
	ErrNotValidReq = errors.New("invalid request")
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
	ToID   uuid.UUID `json:"to_id,omitempty"`
	Status string    `json:"status,omitempty"`
}

type FriendMetaData struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name"`
}
type CacheUpdateStruct struct {
	UserID        uuid.UUID
	ReqID         uuid.UUID
	OtherUserInfo FriendMetaData
	Lable         string
}

type CacheRsDeleteStruct struct {
	UserID uuid.UUID
	ReqID  uuid.UUID
	Lable  string
}

type FriendReq struct {
	ReqID      uuid.UUID
	FromID     uuid.UUID
	ToID       uuid.UUID
	UpdateTime time.Time
}
type CancelFriendReq struct {
	FromID     uuid.UUID
	ReqID      uuid.UUID
	UpdateTime time.Time
}

type CacheUpdateFriStruct struct {
	UserID uuid.UUID
	ToID   FriendMetaData
	Lable  string
}

type GetReqList struct {
	PendingIDsList *map[uuid.UUID]FriendMetaData
	RequestIDsList *map[uuid.UUID]FriendMetaData
}

type ResponseReqList struct {
	PendingIDsList map[uuid.UUID]FriendMetaData `json:"pending_ids"`
	RequestIDsList map[uuid.UUID]FriendMetaData `json:"request_ids"`
}

type ResponseFriListStruct struct {
	FriendList []FriendMetaData `json:"id_list"`
}

type ReesponseForAddFriend struct {
	ReqID uuid.UUID `json:"req_id"`
}

type DeleteFirReqStruct struct {
	ReqID  uuid.UUID
	FromID uuid.UUID
}

type FoundUserListRes struct {
	UserList []User
}
type ColorField struct {
	MainColor     string `json:"main_color"`
	FallBackColor string `json:"fallback_color"`
}
type PositionField struct {
	X string `json:"x"`
	Y string `json:"y"`
}

type EleConfig struct {
	Color    ColorField    `json:"color"`
	Position PositionField `json:"position"`
}

type ElementCustom struct {
	Label  string    `json:"label"`
	Config EleConfig `json:"config"`
}
type ConfigList struct {
	List []ElementCustom `json:"config_list,omitempty"`
}
type JobForSaveConfig struct {
	UserID     uuid.UUID
	ConfigList *ConfigList
}

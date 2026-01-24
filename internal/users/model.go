package users

import (
	"time"

	"github.com/google/uuid"
)

type User struct{
	ID uuid.UUID  `json:"id"`
	Name string `json:"name"`	
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email string `json:"email"`
	IsRED bool `json:"is_chirpy_red"`
}


type CreateUserInput struct{
	Name string
	Email string 
	Password string 
}


type UpdateUserPassword struct{
	UserID uuid.UUID 
	Password string 
}


type DefaultUsersParameters struct{
		Name string `json:"name"`
		Email string `json:"email"`
		Password string `json:"password"`
}

type StatusFriendParameters struct{
	ToID uuid.UUID `json:"to_id"`
	Status string  `json:"status"`
}

type CacheUpdateStruct struct{
	UserID uuid.UUID
	ReqID uuid.UUID
	Lable string
}

type FriendReq struct{
	ReqID uuid.UUID
	FromID uuid.UUID
	ToID uuid.UUID
}

type CacheUpdateFriStruct struct{
	UserID uuid.UUID
	ToID uuid.UUID
	Lable string
}

type GetReqList struct{
	PendingIDsList *[]uuid.UUID 
	RequestIDsList *[]uuid.UUID 
}

type ResponseReqList struct{
	PendingIDsList []uuid.UUID
	RequestIDsList []uuid.UUID
}

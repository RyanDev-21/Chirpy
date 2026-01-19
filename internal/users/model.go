package users

import (
	"github.com/google/uuid"
	"time"
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

type AddFriendParameters struct{
	ToID uuid.UUID `json:"to_id"`
	Type string  `json:"type"`
}

type CacheUpdateStruct struct{
	Label string
	UserID uuid.UUID
	toID uuid.UUID
}

type FriendReq struct{
	FromID uuid.UUID
	ToID uuid.UUID
}

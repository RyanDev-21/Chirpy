package users

import (
	"github.com/google/uuid"
	"time"
)

type User struct{
	ID uuid.UUID  `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email string `json:"email"`
	IsRED bool `json:"is_chirpy_red"`
}


type CreateUserInput struct{
	Email string 
	Password string 
}


type UpdateUserPassword struct{
	UserID uuid.UUID 
	Password string 
}


type DefaultUsersParameters struct{
		Email string `json:"email"`
		Password string `json:"password"`
}

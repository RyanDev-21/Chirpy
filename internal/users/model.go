package users

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUserNotFound   = errors.New("no user found")
	ErrDuplicateEmail = errors.New("duplicate error")
	ErrDuplicateName  = errors.New("duplicate name error")
	ErrInvalidInput   = errors.New("invalid input")
	// ErrNoPassword: the account signs in with Google only
	ErrNoPassword = errors.New("account has no password")
)

type User struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email     string    `json:"email"`
	IsRED     bool      `json:"is_chirpy_red"`
	// Verified is true once the email address has been confirmed.
	Verified bool `json:"email_verified"`
}

// PublicUser is what other users may see (no email).
type PublicUser struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
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

type PasswordUpdateStruct struct {
	OldPass string `json:"old_password"`
	NewPass string `json:"new_password"`
}

type FoundUserListRes struct {
	UserList []PublicUser
}

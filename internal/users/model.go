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
	List []ElementCustom `json:"config_list"`
}

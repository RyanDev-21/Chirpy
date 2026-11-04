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
	ErrBioTooLong = errors.New("bio must be at most 150 characters")
)

// RenameEvery is how often a user may change their username.
const RenameEvery = 30 * 24 * time.Hour

const maxBioLen = 150 // users.bio VARCHAR(150)

// RenameTooSoonError: the username was changed less than RenameEvery ago.
type RenameTooSoonError struct{ Until time.Time }

func (e *RenameTooSoonError) Error() string {
	return "you can change your username again after " + e.Until.Format(time.RFC3339)
}

type User struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email     string    `json:"email"`
	IsRED     bool      `json:"is_chirpy_red"`
	// Verified is true once the email address has been confirmed.
	Verified bool   `json:"email_verified"`
	Bio      string `json:"bio"`
	// NameChangeAvailableAt is when the username may next be changed; null
	// when it can be changed now.
	NameChangeAvailableAt *time.Time `json:"name_change_available_at"`
}

// Profile is another user's public profile.
type Profile struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Bio  string    `json:"bio"`
}

// UpdateProfileRequest is PATCH /api/users/me; absent fields stay.
type UpdateProfileRequest struct {
	Name *string `json:"name"`
	Bio  *string `json:"bio"`
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

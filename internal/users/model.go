package users

import (
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"strconv"
	"time"

	"RyanDev-21.com/Chirpy/internal/catalog"
	"github.com/google/uuid"
)

var (
	ErrUserNotFound   = errors.New("no user found")
	ErrDuplicateEmail = errors.New("duplicate error")
	ErrDuplicateName  = errors.New("duplicate name error")
	ErrInvalidInput   = errors.New("invalid input")
	// ErrNoPassword: the account signs in with Google only
	ErrNoPassword   = errors.New("account has no password")
	ErrBioTooLong   = errors.New("bio must be at most 150 characters")
	ErrBadPlace     = errors.New("location must be an id from GET /api/discover/options")
	ErrBadInterests = errors.New("interests must be up to 10 ids from GET /api/discover/options")
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
	// AvatarURL is null without a picture; it changes with every upload,
	// so clients can cache the image for as long as they like.
	AvatarURL *string `json:"avatar_url"`
	// Location is the place picked from the catalog (null if none).
	Location *catalog.Place `json:"location"`
	// Interests is filled in by /users/me and profile updates only.
	Interests *[]string `json:"interests,omitempty"`
	// Discoverable: shown to others in "people you may know".
	Discoverable bool `json:"discoverable"`
}

// Profile is another user's public profile. Location and interests are
// shown only to friends, or to everyone if the user is discoverable.
type Profile struct {
	ID        uuid.UUID      `json:"id"`
	Name      string         `json:"name"`
	Bio       string         `json:"bio"`
	AvatarURL *string        `json:"avatar_url"`
	Location  *catalog.Place `json:"location"`
	Interests []string       `json:"interests"`
}

// AvatarURL is where a user's picture is served (see avatarURL).
func AvatarURL(id uuid.UUID, at time.Time) *string { return avatarURL(id, at) }

// AvatarURLFrom is AvatarURL for a nullable database column (nil: no picture).
func AvatarURLFrom(id uuid.UUID, at pgtype.Timestamp) *string {
	if !at.Valid {
		return nil
	}
	return avatarURL(id, at.Time)
}

// PlaceOf resolves a stored location id (nil if unset or no longer listed).
func PlaceOf(id string) *catalog.Place {
	if p, ok := catalog.LookupPlace(id); ok {
		return &p
	}
	return nil
}

// avatarURL is where a user's picture is served, versioned by upload time.
func avatarURL(id uuid.UUID, at time.Time) *string {
	u := "/api/users/" + id.String() + "/avatar?v=" + strconv.FormatInt(at.UnixMilli(), 10)
	return &u
}

// UpdateProfileRequest is PATCH /api/users/me; absent fields stay.
// Location "" clears it; Interests replaces the whole list.
type UpdateProfileRequest struct {
	Name         *string   `json:"name"`
	Bio          *string   `json:"bio"`
	Location     *string   `json:"location"`
	Interests    *[]string `json:"interests"`
	Discoverable *bool     `json:"discoverable"`
}

// PublicUser is what other users may see (no email).
type PublicUser struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	AvatarURL *string   `json:"avatar_url"`
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

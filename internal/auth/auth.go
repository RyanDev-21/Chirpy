package auth

import (
	"time"

	"github.com/google/uuid"
)

type responseType struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	CreatedAt    string    `json:"created_at"`
	UpdatedAt    string    `json:"updated_at"`
	Email        string    `json:"email"`
	IsChirpyRed  bool      `json:"is_chirpy_red"`
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token"`
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type PayloadForRefresh struct {
	token     string
	userID    uuid.UUID
	expiresAt time.Time
}

// RefreshToken is a stored refresh token; the token itself is never kept,
// only its hash.
type RefreshToken struct {
	UserID    uuid.UUID
	ExpiresAt time.Time
	UpdatedAt time.Time
}

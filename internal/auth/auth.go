package auth

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var NoUserFoundErr = errors.New("no user found")

type responseType struct {
	ID           uuid.UUID `json:"id"`
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

type RefreshToken struct {
	Token     string
	UserID    uuid.UUID
	ExpiresAt time.Time
	UpdatedAt time.Time
}

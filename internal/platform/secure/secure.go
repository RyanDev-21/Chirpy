package secure

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const issuer = "chirpy"

var (
	ErrPassNotMatch   = errors.New("password incorrect")
	ErrExpiredToken   = errors.New("expired token")
	ErrInvalidToken   = errors.New("invalid token")
	ErrMissingToken   = errors.New("missing bearer token")
	dummyPasswordHash string
)

func init() {
	// used to keep login timing constant when the email does not exist
	dummyPasswordHash, _ = argon2id.CreateHash("chirpy-dummy-password", argon2id.DefaultParams)
}

func HashPassword(password string) (string, error) {
	return argon2id.CreateHash(password, argon2id.DefaultParams)
}

func CheckPassword(password string, hash string) (bool, error) {
	return argon2id.ComparePasswordAndHash(password, hash)
}

// BurnPasswordCheck spends the same time as a real password check.
func BurnPasswordCheck(password string) {
	_, _ = argon2id.ComparePasswordAndHash(password, dummyPasswordHash)
}

func MakeJWT(userID uuid.UUID, tokenSecret string, expiresIn time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(now.Add(expiresIn)),
		Issuer:    issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		Subject:   userID.String(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(tokenSecret))
}

func ValidateJWT(tokenString, tokenSecret string) (uuid.UUID, error) {
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(tokenSecret), nil
	}, jwt.WithIssuer(issuer), jwt.WithExpirationRequired())
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return uuid.Nil, ErrExpiredToken
		}
		return uuid.Nil, ErrInvalidToken
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	return userID, nil
}

// GetBearerToken extracts the token from an "Authorization: Bearer <token>" header.
func GetBearerToken(headers http.Header) (string, error) {
	h := headers.Get("Authorization")
	token, ok := strings.CutPrefix(h, "Bearer ")
	token = strings.TrimSpace(token)
	if !ok || token == "" {
		return "", ErrMissingToken
	}
	return token, nil
}

func MakeRefreshToken() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", errors.New("failed to generate the random data")
	}
	return hex.EncodeToString(key), nil
}

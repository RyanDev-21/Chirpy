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

// Token times are written to the millisecond (iat 1767225600.123), so a
// revocation can tell a token issued just before it from one issued just
// after, such as a login right after a password reset.
func init() { jwt.TimePrecision = time.Millisecond }

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
	id, _, err := ValidateJWTIssued(tokenString, tokenSecret)
	return id, err
}

// ValidateJWTIssued also returns when the token was issued (zero if the
// token has no iat claim).
func ValidateJWTIssued(tokenString, tokenSecret string) (uuid.UUID, time.Time, error) {
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(tokenSecret), nil
	}, jwt.WithIssuer(issuer), jwt.WithExpirationRequired())
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return uuid.Nil, time.Time{}, ErrExpiredToken
		}
		return uuid.Nil, time.Time{}, ErrInvalidToken
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, time.Time{}, ErrInvalidToken
	}
	var issued time.Time
	if claims.IssuedAt != nil {
		issued = claims.IssuedAt.Time
	}
	return userID, issued, nil
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

// adminIssuer marks admin-page tokens. The normal API checks issuer
// "chirpy", so an admin token is useless there and an access token is
// useless on the admin API.
const adminIssuer = "chirpy-admin"

// MakeAdminJWT is a short-lived token for the admin page, issued only after
// password and an emailed code.
func MakeAdminJWT(userID uuid.UUID, tokenSecret string, expiresIn time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(now.Add(expiresIn)),
		Issuer:    adminIssuer,
		IssuedAt:  jwt.NewNumericDate(now),
		Subject:   userID.String(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(tokenSecret))
}

// ValidateAdminJWT checks an admin-page token and returns who and when.
func ValidateAdminJWT(tokenString, tokenSecret string) (uuid.UUID, time.Time, error) {
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(tokenSecret), nil
	}, jwt.WithIssuer(adminIssuer), jwt.WithExpirationRequired())
	if err != nil {
		return uuid.Nil, time.Time{}, ErrInvalidToken
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil || claims.IssuedAt == nil {
		return uuid.Nil, time.Time{}, ErrInvalidToken
	}
	return id, claims.IssuedAt.Time, nil
}

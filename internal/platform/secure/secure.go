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

// passwordParams are OWASP's recommended Argon2id settings (19 MiB, 2
// passes, 1 thread). Hashes made earlier used the library's defaults (64
// MiB): they still verify (each hash names its own settings) and are
// upgraded at the next login (NeedsRehash).
var passwordParams = &argon2id.Params{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32}

// hashing limits how many password hashes run at once: each takes 19-64
// MiB of memory, and a burst of logins must not run a small server out of
// it. Others wait their turn (a hash takes tens of milliseconds).
var hashing = make(chan struct{}, 2)

// hashWait is how long a request waits for a slot before giving up with
// ErrBusy, so a flood of logins can't make requests pile up forever.
var hashWait = 10 * time.Second

// ErrBusy: too many password checks at once; answer 503 and let the
// client retry.
var ErrBusy = errors.New("too many password checks at once, try again shortly")

func withHashSlot[T any](fn func() T) (T, error) {
	t := time.NewTimer(hashWait)
	defer t.Stop()
	select {
	case hashing <- struct{}{}:
	case <-t.C:
		var zero T
		return zero, ErrBusy
	}
	defer func() { <-hashing }()
	return fn(), nil
}

func init() {
	// used to keep login timing constant when the email does not exist
	dummyPasswordHash, _ = argon2id.CreateHash("chirpy-dummy-password", passwordParams)
}

func HashPassword(password string) (string, error) {
	type res struct {
		h   string
		err error
	}
	r, err := withHashSlot(func() res {
		h, err := argon2id.CreateHash(password, passwordParams)
		return res{h, err}
	})
	if err != nil {
		return "", err
	}
	return r.h, r.err
}

func CheckPassword(password string, hash string) (bool, error) {
	type res struct {
		ok  bool
		err error
	}
	r, err := withHashSlot(func() res {
		ok, err := argon2id.ComparePasswordAndHash(password, hash)
		return res{ok, err}
	})
	if err != nil {
		return false, err
	}
	return r.ok, r.err
}

// NeedsRehash reports whether hash was made with older settings.
func NeedsRehash(hash string) bool {
	p, _, _, err := argon2id.DecodeHash(hash)
	return err == nil && (p.Memory != passwordParams.Memory || p.Iterations != passwordParams.Iterations || p.Parallelism != passwordParams.Parallelism)
}

// BurnPasswordCheck spends the same time as a real password check.
func BurnPasswordCheck(password string) {
	_, _ = withHashSlot(func() bool {
		_, _ = argon2id.ComparePasswordAndHash(password, dummyPasswordHash)
		return true
	})
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

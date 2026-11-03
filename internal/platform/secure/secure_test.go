package secure

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPasswordHashing(t *testing.T) {
	pw := "Pw-" + uuid.NewString()[:8] + "!"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := CheckPassword(pw, hash); err != nil || !ok {
		t.Fatalf("correct password rejected: ok=%v err=%v", ok, err)
	}
	if ok, _ := CheckPassword(pw+"x", hash); ok {
		t.Fatal("wrong password accepted")
	}
}

func TestJWT(t *testing.T) {
	secret := uuid.NewString()
	userID := uuid.New()

	tests := []struct {
		name    string
		token   func(t *testing.T) string
		secret  string
		wantErr error
	}{
		{
			name:   "valid",
			token:  func(t *testing.T) string { return mustJWT(t, userID, secret, time.Minute) },
			secret: secret,
		},
		{
			name:    "expired",
			token:   func(t *testing.T) string { return mustJWT(t, userID, secret, -time.Minute) },
			secret:  secret,
			wantErr: ErrExpiredToken,
		},
		{
			name:    "wrong secret",
			token:   func(t *testing.T) string { return mustJWT(t, userID, secret, time.Minute) },
			secret:  uuid.NewString(),
			wantErr: ErrInvalidToken,
		},
		{
			name:    "garbage",
			token:   func(*testing.T) string { return "not.a.jwt" },
			secret:  secret,
			wantErr: ErrInvalidToken,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateJWT(tt.token(t), tt.secret)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && got != userID {
				t.Fatalf("user = %v, want %v", got, userID)
			}
		})
	}
}

func mustJWT(t *testing.T, id uuid.UUID, secret string, ttl time.Duration) string {
	t.Helper()
	tok, err := MakeJWT(id, secret, ttl)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestGetBearerToken(t *testing.T) {
	tests := []struct {
		header  string
		want    string
		wantErr bool
	}{
		{"Bearer abc", "abc", false},
		{"Bearer   abc  ", "abc", false},
		{"bearer abc", "", true},
		{"abc", "", true},
		{"Bearer ", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		h := http.Header{}
		if tt.header != "" {
			h.Set("Authorization", tt.header)
		}
		got, err := GetBearerToken(h)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("GetBearerToken(%q) = %q, %v", tt.header, got, err)
		}
	}
}

func TestMakeRefreshTokenIsRandom(t *testing.T) {
	a, _ := MakeRefreshToken()
	b, _ := MakeRefreshToken()
	if len(a) != 64 || a == b {
		t.Fatalf("unexpected refresh tokens %q %q", a, b)
	}
}

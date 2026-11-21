package secure

import (
	"errors"
	"github.com/alexedwards/argon2id"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
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

// Hashes made with the old (64 MiB) settings still verify, and are marked
// for an upgrade; new ones use the lighter OWASP settings.
func TestPasswordParamsUpgrade(t *testing.T) {
	old, err := argon2id.CreateHash("correct horse", argon2id.DefaultParams)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := CheckPassword("correct horse", old); !ok || err != nil {
		t.Fatalf("old hash: %v %v", ok, err)
	}
	if !NeedsRehash(old) {
		t.Fatal("old hash should be upgraded")
	}
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if NeedsRehash(h) || !strings.Contains(h, "m=19456,t=2,p=1") {
		t.Fatalf("new hash %s", h)
	}
	if ok, _ := CheckPassword("wrong", h); ok {
		t.Fatal("wrong password accepted")
	}
}

// At most two hashes run at once, whatever the number of callers.
func TestHashingIsLimited(t *testing.T) {
	var wg sync.WaitGroup
	var running, peak atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			withHashSlot(func() bool {
				n := running.Add(1)
				for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
				}
				time.Sleep(5 * time.Millisecond)
				running.Add(-1)
				return true
			})
		}()
	}
	wg.Wait()
	if p := peak.Load(); p != 2 {
		t.Fatalf("peak %d concurrent hashes, want 2", p)
	}
}

// With every slot taken, a password check gives up after hashWait with
// ErrBusy instead of waiting forever.
func TestHashingBusy(t *testing.T) {
	old := hashWait
	hashWait = 50 * time.Millisecond
	defer func() { hashWait = old }()
	hashing <- struct{}{}
	hashing <- struct{}{}
	defer func() { <-hashing; <-hashing }()
	start := time.Now()
	if _, err := CheckPassword("x", "y"); !errors.Is(err, ErrBusy) {
		t.Fatalf("err %v", err)
	}
	if _, err := HashPassword("x"); !errors.Is(err, ErrBusy) {
		t.Fatalf("err %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("waited %v", d)
	}
}

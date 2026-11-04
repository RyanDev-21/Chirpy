//go:build integration

package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAccessTokenLasts15Minutes(t *testing.T) {
	e := newEnv(t)
	alice := e.signup("alice")

	parts := strings.Split(alice.Token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", alice.Token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(claims.Exp-claims.Iat) * time.Second; got != 15*time.Minute {
		t.Fatalf("access token lifetime = %v, want 15m", got)
	}
}

func TestCleanupDeletesStaleTokensAndCodes(t *testing.T) {
	e := newEnv(t)
	alice := e.signup("alice")

	// a refresh revokes the old token: recent revocations are kept a day
	var rotated user
	e.expect(e.do("POST", "/api/refresh", alice.RefreshToken, nil), 200).json(t, &rotated)

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	exec(`INSERT INTO refresh_tokens (token, created_at, updated_at, user_id, expire_at)
		VALUES ('expired', $1, $1, $2, $3)`, now.Add(-31*24*time.Hour), alice.ID, now.Add(-time.Hour))
	exec(`INSERT INTO refresh_tokens (token, created_at, updated_at, user_id, expire_at, revoked_at)
		VALUES ('old-revoked', $1, $1, $2, $3, $4)`, now.Add(-3*24*time.Hour), alice.ID, now.Add(27*24*time.Hour), now.Add(-2*24*time.Hour))
	exec(`INSERT INTO email_codes (user_id, purpose, code_hash, expires_at, created_at)
		VALUES ($1, 'reset_password', 'x', $2, $3)`, alice.ID, now.Add(-time.Minute), now.Add(-16*time.Minute))

	if err := e.app.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}

	// left: the live token and the just-revoked one
	e.now(`SELECT count(*)::text FROM refresh_tokens`, "2")
	e.now(`SELECT count(*)::text FROM refresh_tokens WHERE token IN ('expired', 'old-revoked')`, "0")
	e.now(`SELECT count(*)::text FROM email_codes`, "0")
	// the live session still works
	e.expect(e.do("POST", "/api/refresh", rotated.RefreshToken, nil), 200)
}

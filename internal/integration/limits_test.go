//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
)

func TestEmailCodeLastsTwoMinutes(t *testing.T) {
	e := newEnv(t)
	e.expect(e.do("POST", "/api/users", "", map[string]string{"name": "quick", "email": "quick@example.io", "password": password}), 202)
	e.now(`SELECT (expires_at - created_at)::text FROM email_codes`, "00:02:00")
	if m, _ := e.mail.Last("quick@example.io"); !strings.Contains(m.Text, "expires in 2 minutes") {
		t.Fatalf("email text = %q", m.Text)
	}
}

func TestGoogleOnlyAccountCannotChangePassword(t *testing.T) {
	e := newEnv(t)
	var res struct {
		SignupToken string `json:"signup_token"`
	}
	e.expect(e.do("POST", "/api/auth/google", "", map[string]string{"id_token": e.googleToken("g-9", "gg@gmail.com", true)}), 200).json(t, &res)
	var u user
	e.expect(e.do("POST", "/api/auth/google/complete", "", map[string]string{"signup_token": res.SignupToken, "name": "googler"}), 200).json(t, &u)
	body := map[string]string{"old_password": "", "new_password": password}
	e.expect(e.do("POST", "/api/users/password", u.Token, body), 409)
}

func TestChangePasswordRateLimit(t *testing.T) {
	e := newEnv(t)
	u := e.signup("pwuser")
	body := map[string]string{"old_password": "wrong-" + password, "new_password": password}
	e.assertLimited(5, func(int) int { return e.do("POST", "/api/users/password", u.Token, body).code })
}

func TestGroupCreateRateLimit(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser("maker")
	e.assertLimited(10, func(i int) int {
		return e.do("POST", "/api/chats/groups", u.Token, map[string]any{"group_name": fmt.Sprintf("g%d", i), "max_mems": 5}).code
	})
}

func TestReadyzRateLimit(t *testing.T) {
	e := newEnv(t)
	e.assertLimited(30, func(int) int { return e.do("GET", "/readyz", "", nil).code })
	e.expect(e.do("GET", "/livez", "", nil), 204) // keep-alive pings are never limited
}

func TestAuthenticatedRoutesShareABudget(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser("reader")
	r := e.expect(e.do("GET", "/api/users/me", u.Token, nil), 200)
	// /users/me has no route policy of its own, so the header is the shared one
	if got := r.header.Get("X-RateLimit-Limit"); got != "300" {
		t.Fatalf("X-RateLimit-Limit = %q, want 300", got)
	}
}

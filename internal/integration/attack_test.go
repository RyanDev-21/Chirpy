//go:build integration

package integration

import (
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
)

// public routes: everything else must refuse unauthenticated callers
var publicRoutes = map[string]bool{
	"GET /livez": true, "GET /readyz": true, "GET /healthz": true,
	"POST /api/users": true, "POST /api/login": true, "POST /api/refresh": true, "POST /api/revoke": true,
	"POST /api/auth/verify-email": true, "POST /api/auth/resend-verification": true,
	"POST /api/auth/password-reset": true, "POST /api/auth/password-reset/confirm": true,
	"POST /api/auth/google": true, "POST /api/auth/google/complete": true,
}

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// forged access tokens an attacker might try
func forgedTokens(t *testing.T, u user) map[string]string {
	t.Helper()
	claims := jwt.RegisteredClaims{Issuer: "chirpy-access", Subject: u.ID, IssuedAt: jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}
	wrongKey, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("not-the-server-secret-0123456789abcdef"))
	expiredClaims := claims
	expiredClaims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	expired, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, expiredClaims).SignedString([]byte(secret))
	enc := base64.RawURLEncoding.EncodeToString
	none := enc([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + enc([]byte(`{"iss":"chirpy-access","sub":"`+u.ID+`","exp":4102444800}`)) + "."
	// a valid token with its payload swapped for another user's id
	parts := strings.Split(u.Token, ".")
	swapped := parts[0] + "." + enc([]byte(`{"iss":"chirpy-access","sub":"00000000-0000-0000-0000-000000000001","exp":4102444800}`)) + "." + parts[2]
	return map[string]string{
		"none": "", "garbage": "not-a-token", "wrong key": wrongKey, "expired": expired,
		"alg none": none, "payload swapped": swapped, "refresh token": u.RefreshToken,
	}
}

// TestEveryPrivateRouteRefusesBadTokens walks the real router, so routes
// added later are covered without editing this test.
func TestEveryPrivateRouteRefusesBadTokens(t *testing.T) {
	e := newEnv(t)
	alice := e.signup("alice")
	routes, ok := e.app.Router().(chi.Routes)
	if !ok {
		t.Fatal("router is not a chi router")
	}
	tokens := forgedTokens(t, alice)
	checked := 0
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if publicRoutes[method+" "+route] || method == "OPTIONS" || method == "HEAD" {
			return nil
		}
		path := pathParam.ReplaceAllString(route, "00000000-0000-0000-0000-000000000000")
		for name, tok := range tokens {
			if got := e.do(method, path, tok, map[string]string{}); got.code != http.StatusUnauthorized {
				t.Errorf("%s %s with %s token: %d, want 401 (%s)", method, route, name, got.code, got.body)
			}
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 40 {
		t.Fatalf("only %d private routes found; is the walk broken?", checked)
	}
}

// TestDeletedAccountTokenStopsWorking: the access token of a deleted
// account must not keep acting for up to 15 minutes.
func TestDeletedAccountTokenStopsWorking(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	e.expect(e.do("DELETE", "/api/users/me", alice.Token, map[string]string{"password": password}), 204)
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/users/me", nil},
		{"GET", "/api/friends", nil},
		{"POST", "/api/friends/requests", map[string]string{"to_id": bob.ID}},
		{"POST", "/api/chats/groups", map[string]any{"group_name": "ghost", "max_mems": 5}},
		{"POST", "/api/chats/message", map[string]string{"msg": "boo", "to_id": bob.ID, "type": "private"}},
		{"PATCH", "/api/users/me", map[string]string{"bio": "still here"}},
	} {
		if got := e.do(r.method, r.path, alice.Token, r.body); got.code != http.StatusUnauthorized {
			t.Errorf("%s %s after account deletion: %d, want 401 (%s)", r.method, r.path, got.code, got.body)
		}
	}
}

// TestNoAccessToOthersResources tries every id-taking route as an outsider.
func TestNoAccessToOthersResources(t *testing.T) {
	e := newEnv(t)
	alice, bob, mallory := e.signup("alice"), e.signup("bobby"), e.signup("mallory")
	e.befriend(alice, bob)
	dm := e.send(alice, bob.ID, "private", "secret")
	g := e.adminGroup("private-club", alice, bob)
	gm := e.send(alice, g, "public", "members only")
	var req struct {
		ReqID string `json:"req_id"`
	}
	carol := e.seedUser("carol")
	e.expect(e.do("POST", "/api/friends/requests", alice.Token, map[string]string{"to_id": carol.ID}), 200).json(t, &req)

	cases := []struct {
		method, path string
		body         any
		want         []int
	}{
		{"GET", "/api/chats/groups/" + g, nil, []int{404}},
		{"GET", "/api/chat/groups/" + g + "/messages", nil, []int{403, 404}},
		{"POST", "/api/chat/groups/" + g + "/messages", map[string]string{"msg_id": gm}, []int{403, 404}},
		{"POST", "/api/chats/message", map[string]string{"msg": "x", "to_id": g, "type": "public"}, []int{403, 404}},
		{"GET", "/api/chats/groups/" + g + "/invite", nil, []int{404}},
		{"POST", "/api/chats/groups/" + g + "/invite", nil, []int{404}},
		{"PATCH", "/api/chats/groups/" + g, map[string]string{"group_name": "pwned"}, []int{404}},
		{"DELETE", "/api/chats/groups/" + g, nil, []int{404}},
		{"DELETE", "/api/chats/groups/" + g + "/members/" + bob.ID, nil, []int{404}},
		{"PUT", "/api/chats/groups/" + g + "/leader", map[string]string{"user_id": mallory.ID}, []int{404}},
		{"PUT", "/api/chat/messages/" + dm + "/reaction", map[string]string{"emoji": "👍"}, []int{404}},
		{"DELETE", "/api/chat/messages/" + dm + "/reaction", nil, []int{404}},
		{"GET", "/api/chat/messages/" + dm + "/reactions", nil, []int{404}},
		{"PATCH", "/api/chat/messages/" + dm, map[string]string{"msg": "forged"}, []int{404}},
		{"DELETE", "/api/chat/messages/" + dm, nil, []int{404}},
		{"PUT", "/api/chat/messages/" + gm + "/reaction", map[string]string{"emoji": "👍"}, []int{404}},
		{"PATCH", "/api/chat/messages/" + gm, map[string]string{"msg": "forged"}, []int{404}},
		{"POST", "/api/chat/" + alice.ID + "/messages", map[string]string{"msg_id": dm}, []int{400, 404}}, // seen on someone else's message
		{"PUT", "/api/friends/requests/" + req.ReqID, map[string]string{"status": "confirm"}, []int{404}},
		{"DELETE", "/api/friends/requests/" + req.ReqID, nil, []int{404}},
		{"DELETE", "/api/friends/" + alice.ID, nil, []int{404}},
	}
	for _, c := range cases {
		got := e.do(c.method, c.path, mallory.Token, c.body)
		ok := false
		for _, w := range c.want {
			ok = ok || got.code == w
		}
		if !ok {
			t.Errorf("mallory %s %s: %d, want one of %v (%s)", c.method, c.path, got.code, c.want, got.body)
		}
	}
	// nothing changed
	e.now(`SELECT content FROM message WHERE id = $1`, "secret", dm)
	e.now(`SELECT name FROM chat_groups WHERE id = $1`, "private-club", g)
	e.now(`SELECT count(*)::text FROM member_table WHERE group_id = $1`, "2", g)
	e.now(`SELECT status FROM user_relationships WHERE id = $1`, "pending", req.ReqID)
}

// TestMalformedInputIsRejectedCleanly: wrong types, huge bodies and odd
// text must give 4xx, never 500.
func TestMalformedInputIsRejectedCleanly(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	huge := strings.Repeat("x", 2<<20)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/chats/message", map[string]any{"msg": 12, "to_id": []int{1}, "type": true}},
		{"POST", "/api/chats/message", map[string]string{"msg": huge, "to_id": bob.ID, "type": "private"}},
		{"POST", "/api/chats/message", map[string]string{"msg": "x", "to_id": bob.ID, "type": "private', 'x'); DROP TABLE message;--"}},
		{"POST", "/api/chats/groups", map[string]any{"group_name": map[string]int{"a": 1}, "max_mems": "lots"}},
		{"POST", "/api/chats/groups", map[string]any{"group_name": "g", "max_mems": 99999999}},
		{"PATCH", "/api/users/me", map[string]any{"interests": strings.Repeat("a", 100)}},
		{"PATCH", "/api/users/me", map[string]any{"location": []string{"th-bangkok"}}},
		{"GET", "/api/chat/" + bob.ID + "/messages?limit=-5&before=not-a-uuid", nil},
		{"GET", "/api/chat/" + bob.ID + "/messages?since=yesterday", nil},
		{"GET", "/api/chat/not-a-uuid/messages", nil},
		{"POST", "/api/users", map[string]string{"name": "x", "email": strings.Repeat("a", 500) + "@x.io", "password": password}},
		{"POST", "/api/login", huge},
	} {
		got := e.do(c.method, c.path, alice.Token, c.body)
		if got.code >= 500 || got.code < 400 {
			t.Errorf("%s %s: %d (%s)", c.method, c.path, got.code, got.body)
		}
	}
	e.now(`SELECT count(*)::text FROM message`, "0") // and nothing slipped in

	// injection-looking search text is just text: no error, no rows
	var res struct {
		UserList []any `json:"UserList"`
	}
	e.expect(e.do("GET", "/api/users/search?q="+strings.Repeat("%27%20OR%201%3D1", 3), alice.Token, nil), 200).json(t, &res)
	if len(res.UserList) != 0 {
		t.Fatalf("injection search returned %v", res.UserList)
	}
}

// TestUsernamesRejectInvisibleCharacters: zero-width and direction-override
// characters let one name look like another (impersonation).
func TestUsernamesRejectInvisibleCharacters(t *testing.T) {
	e := newEnv(t)
	alice := e.seedUser("alice")
	for _, name := range []string{"ad\u200bmin", "alice\u202e", "\u2066root", "bob\u00ad", "tab\tname", "ctl\u0007"} {
		if got := e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"name": name}); got.code != 400 {
			t.Errorf("name %q: %d, want 400", name, got.code)
		}
	}
}

// TestSecurityHeaders on API responses.
func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	alice := e.seedUser("alice")
	r := e.do("GET", "/api/users/me", alice.Token, nil)
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := r.header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if r.header.Get("Strict-Transport-Security") == "" {
		t.Error("no Strict-Transport-Security")
	}
}

// TestPasswordChangeEndsOtherSessions: a stolen access token stops working
// once the owner changes their password; signing in again right away works.
func TestPasswordChangeEndsOtherSessions(t *testing.T) {
	e := newEnv(t)
	alice := e.signup("alice")
	stolen := alice.Token
	newPw := password + "-2"
	e.expect(e.do("POST", "/api/users/password", alice.Token, map[string]string{"old_password": password, "new_password": newPw}), 200)
	e.expect(e.do("GET", "/api/users/me", stolen, nil), 401)
	e.expect(e.do("POST", "/api/refresh", alice.RefreshToken, nil), 401)
	var fresh user
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": alice.email, "password": newPw}), 200).json(t, &fresh)
	e.expect(e.do("GET", "/api/users/me", fresh.Token, nil), 200)
}

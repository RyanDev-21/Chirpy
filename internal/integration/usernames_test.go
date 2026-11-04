//go:build integration

package integration

import (
	"strconv"
	"strings"
	"testing"
)

func TestUsernamesAndDisplayNames(t *testing.T) {
	e := newEnv(t)
	alice := e.signup("alice")
	// usernames are a-z 0-9 _ . and unique ignoring case
	e.expect(e.do("POST", "/api/users", "", map[string]string{"name": "Has Space", "email": "x@example.io", "password": password}), 400)
	for _, bad := range []string{"dash-ed", "ထက်လင်း"} { // (each PATCH counts against 10/min)
		e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"name": bad}), 400)
	}
	// "ALICE" is "alice": same answer as any taken name (no enumeration on sign-up)
	r := e.do("POST", "/api/users", "", map[string]string{"name": "ALICE", "email": "other@example.io", "password": password})
	if r.code != 202 && r.code != 409 {
		t.Fatalf("duplicate username in another case: %d %s", r.code, r.body)
	}
	e.now(`SELECT count(*)::text FROM users WHERE name = 'alice'`, "1")

	var me struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
	}
	e.expect(e.do("GET", "/api/users/me", alice.Token, nil), 200).json(t, &me)
	if me.Name != "alice" || me.DisplayName != "alice" {
		t.Fatalf("defaults %+v", me)
	}
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"display_name": "a\u200bdmin"}), 400)
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"display_name": strings.Repeat("x", 51)}), 400)
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"display_name": "  Alice Wong 🌸 "}), 200).json(t, &me)
	if me.DisplayName != "Alice Wong 🌸" || me.Name != "alice" {
		t.Fatalf("after update %+v", me)
	}
	// any time, not limited like usernames
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"display_name": "Ali"}), 200)
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"display_name": "Alice Wong 🌸"}), 200)
	// renaming normalises case
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"name": "Alice.W"}), 200).json(t, &me)
	if me.Name != "alice.w" {
		t.Fatalf("username %q", me.Name)
	}

	// found by either name; shown with both
	bob := e.signup("bobby")
	for _, q := range []string{"alice.w", "Wong"} {
		body := string(e.expect(e.do("GET", "/api/users/search?q="+q, bob.Token, nil), 200).body)
		if !strings.Contains(body, `"name":"alice.w"`) || !strings.Contains(body, `"display_name":"Alice Wong 🌸"`) {
			t.Fatalf("search %q: %s", q, body)
		}
	}
	// friends see the display name (cache refreshed) and push uses it
	e.befriend(alice, bob)
	if body := string(e.expect(e.do("GET", "/api/friends", bob.Token, nil), 200).body); !strings.Contains(body, "Alice Wong 🌸") {
		t.Fatalf("friend list %s", body)
	}
	e.device(bob, "tok-bob")
	e.send(alice, bob.ID, "private", "hi")
	if m := e.waitPush("tok-bob", 1)[0]; m.Title != "Alice Wong 🌸" {
		t.Fatalf("push title %q", m.Title)
	}
}

func TestRetryAfterOn429(t *testing.T) {
	e := newEnv(t)
	body := map[string]string{"email": "x@example.io", "password": "wrong-" + password}
	var last resp
	for range 15 {
		if last = e.do("POST", "/api/login", "", body); last.code == 429 {
			break
		}
	}
	if last.code != 429 {
		t.Fatal("no 429")
	}
	secs, err := strconv.Atoi(last.header.Get("Retry-After"))
	if err != nil || secs < 1 || secs > 60 {
		t.Fatalf("Retry-After %q", last.header.Get("Retry-After"))
	}
}

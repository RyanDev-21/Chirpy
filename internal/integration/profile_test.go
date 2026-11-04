//go:build integration

package integration

import (
	"testing"
	"time"
)

type profileRes struct {
	Name         string     `json:"name"`
	Bio          string     `json:"bio"`
	NextRenameAt *time.Time `json:"name_change_available_at"`
}

func TestRenameAndBio(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	// warm bob's cached friend list, which shows alice's name
	e.expect(e.do("GET", "/api/friends", bob.Token, nil), 200)

	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{}), 400)
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"name": "has space"}), 400)
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"name": "bobby"}), 409)

	var me profileRes
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"name": "alice2", "bio": "hi there"}), 200).json(t, &me)
	if me.Name != "alice2" || me.Bio != "hi there" || me.NextRenameAt == nil || me.NextRenameAt.Before(time.Now().Add(29*24*time.Hour)) {
		t.Fatalf("after update %+v", me)
	}
	// bio alone is fine any time; a second rename within 30 days is not
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"bio": "changed"}), 200)
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"name": "alice3"}), 403)
	// login by email still works, and bob's friend list shows the new name
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": alice.email, "password": password}), 200)
	var friends struct {
		List []struct {
			Name string `json:"name"`
		} `json:"id_list"`
	}
	e.expect(e.do("GET", "/api/friends", bob.Token, nil), 200).json(t, &friends)
	if len(friends.List) != 1 || friends.List[0].Name != "alice2" {
		t.Fatalf("bob's friends %+v", friends)
	}

	// 30 days later
	if _, err := e.db.Exec(`UPDATE users SET name_changed_at = name_changed_at - interval '31 days' WHERE id = $1`, alice.ID); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"name": "alice3"}), 200)
}

func TestPublicProfile(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]string{"bio": "hello"}), 200)

	var p map[string]any
	e.expect(e.do("GET", "/api/users/"+alice.ID, bob.Token, nil), 200).json(t, &p)
	if p["name"] != "alice" || p["bio"] != "hello" {
		t.Fatalf("profile %v", p)
	}
	if _, ok := p["email"]; ok {
		t.Fatalf("profile leaks email: %v", p)
	}
	e.expect(e.do("GET", "/api/users/00000000-0000-0000-0000-000000000001", bob.Token, nil), 404)
	e.expect(e.do("POST", "/api/users/blocks", alice.Token, map[string]string{"user_id": bob.ID}), 204)
	e.expect(e.do("GET", "/api/users/"+alice.ID, bob.Token, nil), 404)
	e.expect(e.do("GET", "/api/users/"+bob.ID, alice.Token, nil), 404)
}

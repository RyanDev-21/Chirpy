//go:build integration

package integration

import (
	"testing"
)

func TestSignupNeedsEmailVerification(t *testing.T) {
	e := newEnv(t)
	email := "new@example.io"
	e.expect(e.do("POST", "/api/users", "", map[string]string{"name": "newbie", "email": email, "password": password}), 202)
	code := e.lastCode(email)

	// unverified: the right password is refused with a clear reason
	r := e.expect(e.do("POST", "/api/login", "", map[string]string{"email": email, "password": password}), 403)
	if string(r.body) != `{"error":"email not verified"}` {
		t.Fatalf("body = %s", r.body)
	}
	// wrong codes are rejected; the fifth kills the code
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for range 5 {
		e.expect(e.do("POST", "/api/auth/verify-email", "", map[string]string{"email": email, "code": wrong}), 400)
	}
	e.expect(e.do("POST", "/api/auth/verify-email", "", map[string]string{"email": email, "code": code}), 400)

	// a resend inside a minute is skipped, so clear the cooldown by expiring the row
	if _, err := e.db.Exec(`UPDATE email_codes SET created_at = NOW() - interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("POST", "/api/auth/resend-verification", "", map[string]string{"email": email}), 202)
	var u user
	e.expect(e.do("POST", "/api/auth/verify-email", "", map[string]string{"email": email, "code": e.lastCode(email)}), 200).json(t, &u)
	if u.Token == "" || u.RefreshToken == "" {
		t.Fatal("verifying should sign in")
	}
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": email, "password": password}), 200)

	// codes are stored as HMACs, never as the code itself
	e.now(`SELECT count(*)::text FROM email_codes`, "0")
}

func TestResendIsQuietAndThrottled(t *testing.T) {
	e := newEnv(t)
	email := "slow@example.io"
	e.expect(e.do("POST", "/api/users", "", map[string]string{"name": "slowpoke", "email": email, "password": password}), 202)
	for range 3 {
		e.expect(e.do("POST", "/api/auth/resend-verification", "", map[string]string{"email": email}), 202)
	}
	if n := e.mail.Count(email); n != 1 {
		t.Fatalf("%d emails sent within a minute, want 1", n)
	}
	// unknown email: same answer, nothing sent
	e.expect(e.do("POST", "/api/auth/resend-verification", "", map[string]string{"email": "ghost@example.io"}), 202)
	if e.mail.Count("ghost@example.io") != 0 {
		t.Fatal("no email for unknown addresses")
	}
}

func TestPasswordReset(t *testing.T) {
	e := newEnv(t)
	u := e.signup("forgetful")
	e.expect(e.do("POST", "/api/auth/password-reset", "", map[string]string{"email": "nobody@example.io"}), 202)
	e.expect(e.do("POST", "/api/auth/password-reset", "", map[string]string{"email": u.email}), 202)
	code := e.lastCode(u.email)

	newPw := password + "-new"
	e.expect(e.do("POST", "/api/auth/password-reset/confirm", "", map[string]string{"email": u.email, "code": "999999", "new_password": newPw}), 400)
	e.expect(e.do("POST", "/api/auth/password-reset/confirm", "", map[string]string{"email": u.email, "code": code, "new_password": "weak"}), 400)
	e.expect(e.do("POST", "/api/auth/password-reset/confirm", "", map[string]string{"email": u.email, "code": code, "new_password": newPw}), 204)

	e.expect(e.do("POST", "/api/refresh", u.RefreshToken, nil), 401) // sessions signed out
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": u.email, "password": password}), 401)
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": u.email, "password": newPw}), 200)
}

func TestGoogleSignIn(t *testing.T) {
	e := newEnv(t)

	// new user: pick a username first
	var res struct {
		Status      string `json:"status"`
		SignupToken string `json:"signup_token"`
		Token       string `json:"token"`
		ID          string `json:"id"`
	}
	e.expect(e.do("POST", "/api/auth/google", "", map[string]string{"id_token": e.googleToken("g-1", "Gina@Gmail.com", true)}), 200).json(t, &res)
	if res.Status != "username_required" || res.SignupToken == "" || res.Token != "" {
		t.Fatalf("first sign-in = %+v", res)
	}
	e.expect(e.do("POST", "/api/auth/google/complete", "", map[string]string{"signup_token": res.SignupToken, "name": "x"}), 400)
	var u user
	e.expect(e.do("POST", "/api/auth/google/complete", "", map[string]string{"signup_token": res.SignupToken, "name": "gina"}), 200).json(t, &u)
	// replaying the signup token cannot create a second account
	e.expect(e.do("POST", "/api/auth/google/complete", "", map[string]string{"signup_token": res.SignupToken, "name": "gina2"}), 401)
	e.now(`SELECT email FROM users WHERE id = $1`, "gina@gmail.com", u.ID)

	// next time: straight in, same account; no password sign-in
	var again user
	e.expect(e.do("POST", "/api/auth/google", "", map[string]string{"id_token": e.googleToken("g-1", "gina@gmail.com", true)}), 200).json(t, &again)
	if again.ID != u.ID || again.Token == "" {
		t.Fatalf("second sign-in = %+v", again)
	}
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": "gina@gmail.com", "password": ""}), 401)

	// Google accounts with an unverified email are refused
	e.expect(e.do("POST", "/api/auth/google", "", map[string]string{"id_token": e.googleToken("g-2", "x@gmail.com", false)}), 401)
	e.expect(e.do("POST", "/api/auth/google", "", map[string]string{"id_token": "forged"}), 401)
}

func TestGoogleLinksExistingAccounts(t *testing.T) {
	e := newEnv(t)

	// verified password account: Google links to it
	owner := e.signup("owner")
	var linked user
	e.expect(e.do("POST", "/api/auth/google", "", map[string]string{"id_token": e.googleToken("g-owner", owner.email, true)}), 200).json(t, &linked)
	if linked.ID != owner.ID {
		t.Fatal("Google should sign in to the existing verified account")
	}
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": owner.email, "password": password}), 200)

	// unverified account squatting someone's email: Google proves the real
	// owner, so the squatter's password stops working
	victim := "victim@example.io"
	e.expect(e.do("POST", "/api/users", "", map[string]string{"name": "squatter", "email": victim, "password": password}), 202)
	var taken user
	e.expect(e.do("POST", "/api/auth/google", "", map[string]string{"id_token": e.googleToken("g-victim", victim, true)}), 200).json(t, &taken)
	if taken.Token == "" {
		t.Fatal("the real owner should be signed in")
	}
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": victim, "password": password}), 401)
}

func TestDeleteAccount(t *testing.T) {
	e := newEnv(t)
	leader, member, friend := e.signup("leader"), e.signup("member"), e.signup("friend")

	// a friendship, a shared group (leader -> member takes over), and a solo group (deleted)
	var r struct {
		ReqID string `json:"req_id"`
	}
	e.expect(e.do("POST", "/api/friends/requests", leader.Token, map[string]string{"to_id": friend.ID}), 200).json(t, &r)
	e.expect(e.do("PUT", "/api/friends/requests/"+r.ReqID, friend.Token, map[string]string{"status": "confirm"}), 201)
	var shared, solo struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", leader.Token, map[string]any{"group_name": "shared", "max_mems": 5}), 201).json(t, &shared)
	e.expect(e.do("POST", "/api/chats/groups", leader.Token, map[string]any{"group_name": "solo", "max_mems": 5}), 201).json(t, &solo)
	e.expect(e.join(member.Token, e.invite(leader.Token, shared.ChatID)), 201)
	e.expect(e.do("POST", "/api/chats/message", leader.Token, map[string]string{"msg": "bye", "to_id": friend.ID, "type": "private"}), 200)
	e.expect(e.do("GET", "/api/friends", friend.Token, nil), 200) // warm the cache

	e.expect(e.do("DELETE", "/api/users/me", leader.Token, map[string]string{"password": "wrong-" + password}), 401)
	e.expect(e.do("DELETE", "/api/users/me", leader.Token, map[string]string{}), 401)
	e.expect(e.do("DELETE", "/api/users/me", leader.Token, map[string]string{"password": password}), 204)

	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": leader.email, "password": password}), 401)
	e.expect(e.do("GET", "/api/users/me", leader.Token, nil), 401)
	e.now(`SELECT role FROM member_table WHERE group_id = $1 AND member_id = $2`, "Leader", shared.ChatID, member.ID)
	e.now(`SELECT current_member::text FROM chat_groups WHERE id = $1`, "1", shared.ChatID)
	e.now(`SELECT count(*)::text FROM chat_groups WHERE id = $1`, "0", solo.ChatID)
	// the message stays, without an author
	e.now(`SELECT count(*)::text FROM message WHERE from_id IS NULL AND content = 'bye'`, "1")

	var fl struct {
		List []struct{} `json:"id_list"`
	}
	e.expect(e.do("GET", "/api/friends", friend.Token, nil), 200).json(t, &fl)
	if len(fl.List) != 0 {
		t.Fatalf("deleted user still in the friend list: %+v", fl.List)
	}
	// the new leader really leads (cache reflects the transfer)
	e.expect(e.do("POST", "/api/chats/groups/"+shared.ChatID+"/invite", member.Token, nil), 200)
}

func TestDeleteGoogleOnlyAccount(t *testing.T) {
	e := newEnv(t)
	var res struct {
		SignupToken string `json:"signup_token"`
	}
	e.expect(e.do("POST", "/api/auth/google", "", map[string]string{"id_token": e.googleToken("g-del", "del@gmail.com", true)}), 200).json(t, &res)
	var u user
	e.expect(e.do("POST", "/api/auth/google/complete", "", map[string]string{"signup_token": res.SignupToken, "name": "deleteme"}), 200).json(t, &u)

	other := e.googleToken("g-someone-else", "else@gmail.com", true)
	e.expect(e.do("DELETE", "/api/users/me", u.Token, map[string]string{"google_id_token": other}), 401)
	e.expect(e.do("DELETE", "/api/users/me", u.Token, map[string]string{"google_id_token": e.googleToken("g-del", "del@gmail.com", true)}), 204)
	e.now(`SELECT count(*)::text FROM user_identities`, "0")
}

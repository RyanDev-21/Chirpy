//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestHealth(t *testing.T) {
	e := newEnv(t)
	e.expect(e.do("GET", "/healthz", "", nil), 200)
}

func TestProtectedRoutesRequireAuth(t *testing.T) {
	e := newEnv(t)
	routes := []struct{ method, path string }{
		{"GET", "/api/friends"},
		{"GET", "/api/friends/requests"},
		{"POST", "/api/friends/requests"},
		{"PUT", "/api/friends/requests/00000000-0000-0000-0000-000000000000"},
		{"DELETE", "/api/friends/requests/00000000-0000-0000-0000-000000000000"},
		{"GET", "/api/users/search?q=a"},
		{"POST", "/api/users/password"},
		{"GET", "/api/users/configs"},
		{"POST", "/api/users/configs"},
		{"POST", "/api/chats/groups"},
		{"POST", "/api/chats/groups/00000000-0000-0000-0000-000000000000/members"},
		{"DELETE", "/api/chats/groups/00000000-0000-0000-0000-000000000000/members"},
		{"POST", "/api/chats/message"},
		{"GET", "/api/chat/00000000-0000-0000-0000-000000000000/messages"},
		{"GET", "/api/chat/groups/00000000-0000-0000-0000-000000000000/messages"},
		{"GET", "/api/chats/ws"},
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			if got := e.do(r.method, r.path, "", nil); got.code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", got.code)
			}
			if got := e.do(r.method, r.path, "not-a-token", nil); got.code != http.StatusUnauthorized {
				t.Fatalf("bad token status = %d, want 401", got.code)
			}
		})
	}
	// admin reset only exists on PLATFORM=dev
	if got := e.do("POST", "/admin/reset", "", nil); got.code != http.StatusNotFound {
		t.Fatalf("admin reset status = %d, want 404 outside dev", got.code)
	}
}

func TestAuthLifecycle(t *testing.T) {
	e := newEnv(t)
	newPassword := password + "2"
	alice := e.signup("alice")

	e.expect(e.do("POST", "/api/users", "", map[string]string{"name": "alice2", "email": alice.email, "password": password}), 409)
	e.expect(e.do("POST", "/api/users", "", map[string]string{"name": "alice", "email": "other@example.io", "password": password}), 409)

	// unknown email and wrong password look the same
	wrong := e.expect(e.do("POST", "/api/login", "", map[string]string{"email": alice.email, "password": "wrong-" + password}), 401)
	unknown := e.expect(e.do("POST", "/api/login", "", map[string]string{"email": "ghost@example.io", "password": "wrong-" + password}), 401)
	if string(wrong.body) != string(unknown.body) {
		t.Fatalf("login errors differ: %s vs %s", wrong.body, unknown.body)
	}

	// refresh rotates the token; the old one stops working
	var rotated struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	e.expect(e.do("POST", "/api/refresh", alice.RefreshToken, nil), 200).json(t, &rotated)
	e.expect(e.do("POST", "/api/refresh", alice.RefreshToken, nil), 401)
	e.expect(e.do("GET", "/api/friends", rotated.AccessToken, nil), 200)

	// changing the password revokes every refresh token
	e.expect(e.do("POST", "/api/users/password", alice.Token, map[string]string{"old_password": password, "new_password": newPassword}), 200)
	e.expect(e.do("POST", "/api/refresh", rotated.RefreshToken, nil), 401)
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": alice.email, "password": newPassword}), 200)
}

func TestSearchHidesEmails(t *testing.T) {
	e := newEnv(t)
	alice := e.signup("alice")
	e.signup("alicia")
	r := e.expect(e.do("GET", "/api/users/search?q=alic", alice.Token, nil), 200)
	var raw map[string][]map[string]any
	r.json(t, &raw)
	if len(raw["UserList"]) == 0 {
		t.Fatalf("no search results: %s", r.body)
	}
	for _, u := range raw["UserList"] {
		if _, ok := u["email"]; ok {
			t.Fatalf("search leaked an email: %s", r.body)
		}
	}
}

func TestFriendsAndPrivateChat(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bob")
	bobWS := e.dial(bob.Token)

	// strangers cannot message each other
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]string{"msg": "hi", "to_id": bob.ID, "type": "private"}), 403)

	var sent struct {
		ReqID string `json:"req_id"`
	}
	e.expect(e.do("POST", "/api/friends/requests", alice.Token, map[string]string{"to_id": bob.ID}), 200).json(t, &sent)
	if ev := readEvent(t, bobWS); ev.Event != "AddFri" {
		t.Fatalf("bob got %q, want AddFri", ev.Event)
	}
	e.expect(e.do("POST", "/api/friends/requests", bob.Token, map[string]string{"to_id": alice.ID}), 409)

	e.expect(e.do("PUT", "/api/friends/requests/"+sent.ReqID, alice.Token, map[string]string{"status": "confirm"}), 404)
	e.expect(e.do("PUT", "/api/friends/requests/"+sent.ReqID, bob.Token, map[string]string{"status": "confirm"}), 201)
	e.eventually(`SELECT status FROM user_relationships WHERE id = $1`, "confirm", sent.ReqID)

	for i := range 3 {
		e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]string{"msg": fmt.Sprintf("hello %d", i), "to_id": bob.ID, "type": "private"}), 200)
		ev := readEvent(t, bobWS)
		var m struct {
			Content string `json:"msg"`
			FromID  string `json:"from_id"`
		}
		_ = json.Unmarshal(ev.Payload, &m)
		if ev.Event != "msg" || m.Content != fmt.Sprintf("hello %d", i) || m.FromID != alice.ID {
			t.Fatalf("bob got %+v %+v", ev, m)
		}
	}
	e.eventually(`SELECT count(*)::text FROM message`, "3")

	checkHistory := func() {
		t.Helper()
		var list struct {
			MsgList []struct {
				MsgInfo struct {
					Msg struct {
						Content string `json:"msg"`
					}
				} `json:"message_info"`
			} `json:"msgList"`
		}
		e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &list)
		if len(list.MsgList) != 3 || list.MsgList[0].MsgInfo.Msg.Content != "hello 0" || list.MsgList[2].MsgInfo.Msg.Content != "hello 2" {
			t.Fatalf("history = %+v", list.MsgList)
		}
	}
	checkHistory()

	// with redis wiped, everything is rebuilt from postgres
	if err := e.redis.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	checkHistory()
	var friends struct {
		List []struct {
			Name string `json:"name"`
		} `json:"id_list"`
	}
	e.expect(e.do("GET", "/api/friends", alice.Token, nil), 200).json(t, &friends)
	if len(friends.List) != 1 || friends.List[0].Name != "bob" {
		t.Fatalf("friends after cache loss = %+v", friends.List)
	}
}

func TestFriendRequestDenyAllowsResend(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bob")
	var sent struct {
		ReqID string `json:"req_id"`
	}
	e.expect(e.do("POST", "/api/friends/requests", alice.Token, map[string]string{"to_id": bob.ID}), 200).json(t, &sent)
	e.eventually(`SELECT count(*)::text FROM user_relationships`, "1")
	e.expect(e.do("PUT", "/api/friends/requests/"+sent.ReqID, bob.Token, map[string]string{"status": "cancel"}), 201)
	e.eventually(`SELECT count(*)::text FROM user_relationships`, "0")
	e.expect(e.do("POST", "/api/friends/requests", alice.Token, map[string]string{"to_id": bob.ID}), 200)
}

func TestGroups(t *testing.T) {
	e := newEnv(t)
	leader, member, outsider := e.signup("leader"), e.signup("member"), e.signup("outsider")

	e.expect(e.do("POST", "/api/chats/groups", leader.Token, map[string]any{"group_name": "", "max_mems": 2}), 400)
	var g struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", leader.Token, map[string]any{"group_name": "devs", "description": "d", "max_mems": 2}), 201).json(t, &g)
	e.expect(e.do("POST", "/api/chats/groups", member.Token, map[string]any{"group_name": "DEVS", "max_mems": 2}), 409)
	e.eventually(`SELECT name FROM chat_groups WHERE id = $1`, "devs", g.ChatID)

	members := "/api/chats/groups/" + g.ChatID + "/members"
	memberWS := e.dial(member.Token)
	e.expect(e.do("POST", members, member.Token, nil), 201)
	e.expect(e.do("POST", members, member.Token, nil), 409)
	e.expect(e.do("POST", members, outsider.Token, nil), 409) // full
	e.eventually(`SELECT current_member::text FROM chat_groups WHERE id = $1`, "2", g.ChatID)

	send := map[string]string{"msg": "standup?", "to_id": g.ChatID, "type": "public"}
	e.expect(e.do("POST", "/api/chats/message", outsider.Token, send), 403)
	e.expect(e.do("GET", "/api/chat/groups/"+g.ChatID+"/messages", outsider.Token, nil), 403)
	e.expect(e.do("POST", "/api/chats/message", leader.Token, send), 200)
	if ev := readEvent(t, memberWS); ev.Event != "msg" {
		t.Fatalf("member got %q", ev.Event)
	}
	e.eventually(`SELECT count(*)::text FROM groupmessage WHERE group_id = $1`, "1", g.ChatID)

	e.expect(e.do("DELETE", members, leader.Token, nil), 403)
	e.expect(e.do("DELETE", members, member.Token, nil), 204)
	e.eventually(`SELECT current_member::text FROM chat_groups WHERE id = $1`, "1", g.ChatID)
	e.expect(e.do("POST", "/api/chats/message", member.Token, send), 403)
}

func TestUserConfigPersists(t *testing.T) {
	e := newEnv(t)
	u := e.signup("cfg")
	cfg := map[string]any{"config_list": []map[string]any{{"label": "sidebar", "config": map[string]any{"color": map[string]string{"main_color": "red"}}}}}
	e.expect(e.do("POST", "/api/users/configs", u.Token, cfg), 201)
	e.eventually(`SELECT pref->0->>'label' FROM eleconfig`, "sidebar")
	_ = e.redis.FlushDB(context.Background())
	r := e.expect(e.do("GET", "/api/users/configs", u.Token, nil), 200)
	var got struct {
		List []struct {
			Label string `json:"label"`
		} `json:"config_list"`
	}
	r.json(t, &got)
	if len(got.List) != 1 || got.List[0].Label != "sidebar" {
		t.Fatalf("config after cache loss = %s", r.body)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	body := map[string]string{"email": "x@example.io", "password": "wrong-" + password}
	for range 10 {
		e.expect(e.do("POST", "/api/login", "", body), 401)
	}
	e.expect(e.do("POST", "/api/login", "", body), 429)
}

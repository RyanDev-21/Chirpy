//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"strings"
	"sync"
	"testing"

	"RyanDev-21.com/Chirpy/internal/config"
)

func TestHealth(t *testing.T) {
	e := newEnv(t)
	e.expect(e.do("GET", "/healthz", "", nil), 200)
	e.expect(e.do("GET", "/readyz", "", nil), 200)
	e.expect(e.do("GET", "/livez", "", nil), 204)
}

// free-tier setup: in-process queue (only seen receipts are queued)
func TestMemoryQueueBackend(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.QueueBackend = "memory" })
	alice, bob := e.signup("alice"), e.signup("bob")
	var r struct {
		ReqID string `json:"req_id"`
	}
	e.expect(e.do("POST", "/api/friends/requests", alice.Token, map[string]string{"to_id": bob.ID}), 200).json(t, &r)
	e.expect(e.do("PUT", "/api/friends/requests/"+r.ReqID, bob.Token, map[string]string{"status": "confirm"}), 201)
	var m struct {
		MsgID string `json:"msg_id"`
	}
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]string{"msg": "hi", "to_id": bob.ID, "type": "private"}), 200).json(t, &m)
	e.expect(e.do("POST", "/api/chat/"+alice.ID+"/messages", bob.Token, map[string]string{"msg_id": m.MsgID}), 201)
	e.eventually(`SELECT message_id::text FROM seen_messages`, m.MsgID)
	if n := e.redis.Exists(context.Background(), "jobs:chat.seen.update").Val(); n != 0 {
		t.Fatal("memory backend must not create Redis job streams")
	}
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
		{"POST", "/api/chats/groups"},
		{"GET", "/api/chats/groups"},
		{"GET", "/api/chats/groups/00000000-0000-0000-0000-000000000000"},
		{"POST", "/api/chats/groups/join"},
		{"GET", "/api/chats/groups/invites/ABCD2345"},
		{"GET", "/api/chats/groups/00000000-0000-0000-0000-000000000000/invite"},
		{"POST", "/api/chats/groups/00000000-0000-0000-0000-000000000000/invite"},
		{"DELETE", "/api/chats/groups/00000000-0000-0000-0000-000000000000/members"},
		{"GET", "/api/users/me"},
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

	// a taken email gets the same answer as a new one (no account discovery);
	// the owner is emailed instead
	before := e.mail.Count(alice.email)
	e.expect(e.do("POST", "/api/users", "", map[string]string{"name": "alice2", "email": alice.email, "password": password}), 202)
	if e.mail.Count(alice.email) != before+1 {
		t.Fatal("the existing owner should get a notice email")
	}
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
	e.now(`SELECT status FROM user_relationships WHERE id = $1`, "confirm", sent.ReqID)

	for i := range 3 {
		e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]string{"msg": fmt.Sprintf("hello %d", i), "to_id": bob.ID, "type": "private"}), 200)
		// the message is in postgres as soon as the API answers
		e.now(`SELECT count(*)::text FROM message`, fmt.Sprint(i+1))
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
	e.now(`SELECT count(*)::text FROM user_relationships`, "1")
	e.expect(e.do("PUT", "/api/friends/requests/"+sent.ReqID, bob.Token, map[string]string{"status": "cancel"}), 201)
	e.now(`SELECT count(*)::text FROM user_relationships`, "0")
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
	e.now(`SELECT name FROM chat_groups WHERE id = $1`, "devs", g.ChatID)

	members := "/api/chats/groups/" + g.ChatID + "/members"
	code := e.invite(leader.Token, g.ChatID)
	memberWS := e.dial(member.Token)
	e.expect(e.join(member.Token, code), 201)
	e.expect(e.join(member.Token, code), 409)
	e.expect(e.join(outsider.Token, code), 409) // full
	// knowing a group id is not enough to join: only its leader adds people
	e.expect(e.do("POST", members, outsider.Token, map[string]string{"user_id": outsider.ID}), 404)
	e.now(`SELECT current_member::text FROM chat_groups WHERE id = $1`, "2", g.ChatID)

	send := map[string]string{"msg": "standup?", "to_id": g.ChatID, "type": "public"}
	e.expect(e.do("POST", "/api/chats/message", outsider.Token, send), 403)
	e.expect(e.do("GET", "/api/chat/groups/"+g.ChatID+"/messages", outsider.Token, nil), 403)
	e.expect(e.do("POST", "/api/chats/message", leader.Token, send), 200)
	ev := readEvent(t, memberWS)
	var gm struct {
		GroupID string `json:"group_id"`
		Type    string `json:"type"`
	}
	_ = json.Unmarshal(ev.Payload, &gm)
	if ev.Event != "msg" || gm.Type != "public" || gm.GroupID != g.ChatID {
		t.Fatalf("member got %s %+v, want a public msg for group %s", ev.Event, gm, g.ChatID)
	}
	e.now(`SELECT count(*)::text FROM groupmessage WHERE group_id = $1`, "1", g.ChatID)

	e.expect(e.do("DELETE", members, leader.Token, nil), 403)
	e.expect(e.do("DELETE", members, member.Token, nil), 204)
	e.now(`SELECT current_member::text FROM chat_groups WHERE id = $1`, "1", g.ChatID)
	e.expect(e.do("POST", "/api/chats/message", member.Token, send), 403)
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	body := map[string]string{"email": "x@example.io", "password": "wrong-" + password}
	e.assertLimited(10, func(int) int { return e.do("POST", "/api/login", "", body).code })
}

func TestConcurrentJoinsRespectCapacity(t *testing.T) {
	e := newEnv(t)
	leader := e.signup("leader")
	var g struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", leader.Token, map[string]any{"group_name": "race", "max_mems": 5}), 201).json(t, &g)

	code := e.invite(leader.Token, g.ChatID)
	joiners := make([]user, 20)
	for i := range joiners {
		joiners[i] = e.seedUser(fmt.Sprintf("joiner%d", i))
	}
	codes := make(chan int, len(joiners))
	var wg sync.WaitGroup
	for _, u := range joiners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- e.join(u.Token, code).code
		}()
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for c := range codes {
		counts[c]++
	}
	// the leader holds one of the 5 seats
	if counts[201] != 4 || counts[409] != 16 {
		t.Fatalf("join results = %v, want 4x201 and 16x409", counts)
	}
	e.now(`SELECT count(*)::text FROM member_table WHERE group_id = $1`, "5", g.ChatID)
	e.now(`SELECT current_member::text FROM chat_groups WHERE id = $1`, "5", g.ChatID)
}

func TestConcurrentCrossingFriendRequests(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bob")
	var wg sync.WaitGroup
	codes := make(chan int, 10)
	for i := range 10 {
		from, to := alice, bob
		if i%2 == 1 {
			from, to = bob, alice
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- e.do("POST", "/api/friends/requests", from.Token, map[string]string{"to_id": to.ID}).code
		}()
	}
	wg.Wait()
	close(codes)
	ok := 0
	for c := range codes {
		if c == 200 {
			ok++
		} else if c != 409 {
			t.Fatalf("unexpected status %d", c)
		}
	}
	if ok != 1 {
		t.Fatalf("%d requests succeeded, want exactly 1", ok)
	}
	e.now(`SELECT count(*)::text FROM user_relationships`, "1")
}

func TestReplyMustBeInSameChat(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.signup("alice"), e.signup("bob"), e.signup("carol")
	befriend := func(a, b user) {
		var r struct {
			ReqID string `json:"req_id"`
		}
		e.expect(e.do("POST", "/api/friends/requests", a.Token, map[string]string{"to_id": b.ID}), 200).json(t, &r)
		e.expect(e.do("PUT", "/api/friends/requests/"+r.ReqID, b.Token, map[string]string{"status": "confirm"}), 201)
	}
	befriend(alice, bob)
	befriend(alice, carol)

	var other struct {
		MsgID string `json:"msg_id"`
	}
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]string{"msg": "to carol", "to_id": carol.ID, "type": "private"}), 200).json(t, &other)
	var own struct {
		MsgID string `json:"msg_id"`
	}
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]string{"msg": "to bob", "to_id": bob.ID, "type": "private"}), 200).json(t, &own)

	e.expect(e.do("POST", "/api/chats/message", bob.Token, map[string]string{"msg": "reply", "to_id": alice.ID, "type": "private", "parent_id": other.MsgID}), 400)
	e.expect(e.do("POST", "/api/chats/message", bob.Token, map[string]string{"msg": "reply", "to_id": alice.ID, "type": "private", "parent_id": own.MsgID}), 200)
}

func TestSpoofedForwardedForDoesNotBypassRateLimit(t *testing.T) {
	e := newEnv(t)
	// a different spoofed header on every request must not reset the budget
	e.assertLimited(10, func(i int) int {
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/login", strings.NewReader(`{"email":"x@example.io","password":"nope"}`))
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	})
}

func TestMeEndpoint(t *testing.T) {
	e := newEnv(t)
	u := e.signup("meuser")
	var me struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	e.expect(e.do("GET", "/api/users/me", u.Token, nil), 200).json(t, &me)
	if me.ID != u.ID || me.Name != "meuser" || me.Email != u.email {
		t.Fatalf("me = %+v", me)
	}
	e.expect(e.do("GET", "/api/users/me", "", nil), 401)
}

func TestMyGroupsAndGroupDetail(t *testing.T) {
	e := newEnv(t)
	leader, member, outsider := e.signup("leader"), e.signup("member"), e.signup("outsider")
	var g struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", leader.Token, map[string]any{"group_name": "Beta", "description": "b", "max_mems": 5}), 201).json(t, &g)
	e.expect(e.do("POST", "/api/chats/groups", leader.Token, map[string]any{"group_name": "alpha", "max_mems": 5}), 201)
	e.expect(e.join(member.Token, e.invite(leader.Token, g.ChatID)), 201)

	var mine struct {
		Groups []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Role    string `json:"role"`
			Members int    `json:"member_count"`
		} `json:"groups"`
	}
	e.expect(e.do("GET", "/api/chats/groups", leader.Token, nil), 200).json(t, &mine)
	if len(mine.Groups) != 2 || mine.Groups[0].Name != "alpha" || mine.Groups[1].Role != "Leader" || mine.Groups[1].Members != 2 {
		t.Fatalf("leader's groups = %+v", mine.Groups)
	}
	e.expect(e.do("GET", "/api/chats/groups", member.Token, nil), 200).json(t, &mine)
	if len(mine.Groups) != 1 || mine.Groups[0].ID != g.ChatID || mine.Groups[0].Role != "member" {
		t.Fatalf("member's groups = %+v", mine.Groups)
	}

	var d struct {
		Name     string `json:"name"`
		IsMember bool   `json:"is_member"`
		Count    int    `json:"member_count"`
		Members  []struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	e.expect(e.do("GET", "/api/chats/groups/"+g.ChatID, member.Token, nil), 200).json(t, &d)
	if !d.IsMember || d.Count != 2 || len(d.Members) != 2 || d.Members[0].UserID != leader.ID || d.Members[0].Role != "Leader" {
		t.Fatalf("member's view = %+v", d)
	}
	// groups are private: a non-member can't see the group at all
	e.expect(e.do("GET", "/api/chats/groups/"+g.ChatID, outsider.Token, nil), 404)
	e.expect(e.do("GET", "/api/chats/groups/00000000-0000-0000-0000-000000000000", member.Token, nil), 404)
}

func TestGroupInvites(t *testing.T) {
	e := newEnv(t)
	leader, member, outsider := e.signup("leader"), e.signup("member"), e.signup("outsider")
	var g struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", leader.Token, map[string]any{"group_name": "Devs", "description": "d", "max_mems": 5}), 201).json(t, &g)

	code := e.invite(leader.Token, g.ChatID)
	if len(code) != 9 || code[4] != '-' {
		t.Fatalf("code %q is not XXXX-XXXX", code)
	}
	// outsiders can't read the code
	e.expect(e.do("GET", "/api/chats/groups/"+g.ChatID+"/invite", outsider.Token, nil), 404)

	// preview before joining, typed sloppily
	var p struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Count int    `json:"member_count"`
	}
	loose := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	e.expect(e.do("GET", "/api/chats/groups/invites/"+loose, outsider.Token, nil), 200).json(t, &p)
	if p.ID != g.ChatID || p.Name != "Devs" || p.Count != 1 {
		t.Fatalf("preview = %+v", p)
	}
	e.expect(e.do("GET", "/api/chats/groups/invites/ZZZZ2222", outsider.Token, nil), 404)
	e.expect(e.do("GET", "/api/chats/groups/invites/not-a-code", outsider.Token, nil), 404)

	var joined struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.join(member.Token, code), 201).json(t, &joined)
	if joined.ChatID != g.ChatID {
		t.Fatalf("joined %q, want %q", joined.ChatID, g.ChatID)
	}
	if e.invite(member.Token, g.ChatID) != code {
		t.Fatal("members see the same code")
	}

	// only the leader regenerates; the old code then stops working
	e.expect(e.do("POST", "/api/chats/groups/"+g.ChatID+"/invite", member.Token, nil), 403)
	var fresh struct {
		Code string `json:"code"`
	}
	e.expect(e.do("POST", "/api/chats/groups/"+g.ChatID+"/invite", leader.Token, nil), 200).json(t, &fresh)
	if fresh.Code == code || len(fresh.Code) != 9 {
		t.Fatalf("regenerated code %q", fresh.Code)
	}
	e.expect(e.join(outsider.Token, code), 404)
	e.expect(e.join(outsider.Token, fresh.Code), 201)
}

//go:build integration

package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func (e *env) newGroup(leader user, name string, max int) string {
	e.t.Helper()
	var g struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", leader.Token, map[string]any{"group_name": name, "max_mems": max}), 201).json(e.t, &g)
	return g.ChatID
}

func TestLeaderAddsFriend(t *testing.T) {
	e := newEnv(t)
	leader, friend, other, stranger := e.signup("leader"), e.signup("friend"), e.signup("other"), e.signup("stranger")
	e.befriend(leader, friend)
	e.befriend(leader, other)
	e.befriend(other, friend)
	g := e.newGroup(leader, "crew", 2)
	add := "/api/chats/groups/" + g + "/members"

	e.expect(e.do("POST", add, leader.Token, map[string]string{}), 400)
	e.expect(e.do("POST", add, leader.Token, map[string]string{"user_id": stranger.ID}), 403) // not a friend
	friendWS := e.dial(friend.Token)
	e.expect(e.do("POST", add, leader.Token, map[string]string{"user_id": friend.ID}), 201)
	ev := readEvent(t, friendWS)
	var p struct {
		GroupID string `json:"group_id"`
		ByID    string `json:"by_id"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if ev.Event != "GroupAdded" || p.GroupID != g || p.ByID != leader.ID {
		t.Fatalf("friend got %s %s", ev.Event, ev.Payload)
	}
	e.now(`SELECT current_member::text FROM chat_groups WHERE id = $1`, "2", g)
	e.expect(e.do("POST", add, leader.Token, map[string]string{"user_id": friend.ID}), 409) // already in
	e.expect(e.do("POST", add, leader.Token, map[string]string{"user_id": other.ID}), 409)  // full

	// a member who isn't the leader can't add, even their own friends
	e.expect(e.do("POST", add, friend.Token, map[string]string{"user_id": other.ID}), 403)
	// outsiders can't tell the group exists
	e.expect(e.do("POST", add, stranger.Token, map[string]string{"user_id": stranger.ID}), 404)

	// the added member can chat right away
	e.send(friend, g, "public", "thanks for adding me")

	// a blocked friend is no longer a friend
	g2 := e.newGroup(leader, "crew2", 5)
	e.expect(e.do("POST", "/api/users/blocks", other.Token, map[string]string{"user_id": leader.ID}), 204)
	e.expect(e.do("POST", "/api/chats/groups/"+g2+"/members", leader.Token, map[string]string{"user_id": other.ID}), 403)
}

func TestGroupAvatar(t *testing.T) {
	e := newEnv(t)
	leader, member, outsider := e.signup("leader"), e.signup("member"), e.signup("outsider")
	g := e.newGroup(leader, "crew", 5)
	e.expect(e.join(member.Token, e.invite(leader.Token, g)), 201)
	path := "/api/chats/groups/" + g + "/avatar"

	r, _ := e.doRaw("PUT", path, member.Token, photoWithEXIF(t), nil)
	e.expect(r, 403)
	r, _ = e.doRaw("PUT", path, outsider.Token, photoWithEXIF(t), nil)
	e.expect(r, 404)
	r, _ = e.doRaw("PUT", path, leader.Token, []byte("nope"), nil)
	e.expect(r, 400)
	r, _ = e.doRaw("PUT", path, leader.Token, make([]byte, 6<<20), nil)
	e.expect(r, 413)

	memberWS := e.dial(member.Token)
	r, _ = e.doRaw("PUT", path, leader.Token, photoWithEXIF(t), nil)
	var out struct {
		AvatarURL string `json:"avatar_url"`
	}
	e.expect(r, 200).json(t, &out)
	if !strings.HasPrefix(out.AvatarURL, path+"?v=") {
		t.Fatalf("avatar_url = %q", out.AvatarURL)
	}
	if ev := readEvent(t, memberWS); ev.Event != "GroupUpdated" || !strings.Contains(string(ev.Payload), `"info"`) {
		t.Fatalf("member got %s %s", ev.Event, ev.Payload)
	}

	// members fetch it; it's a re-encoded 256px JPEG without the EXIF
	r, h := e.doRaw("GET", out.AvatarURL, member.Token, nil, nil)
	e.expect(r, 200)
	if h.Get("Content-Type") != "image/jpeg" || strings.Contains(string(r.body), "GPSLatitude") || h.Get("ETag") == "" {
		t.Fatalf("bad avatar response: %v", h)
	}
	r, _ = e.doRaw("GET", path, member.Token, nil, map[string]string{"If-None-Match": h.Get("ETag")})
	e.expect(r, 304)
	r, _ = e.doRaw("GET", path, outsider.Token, nil, nil)
	e.expect(r, 404)

	// listed for members only
	var mine struct {
		Groups []struct {
			AvatarURL *string `json:"avatar_url"`
		} `json:"groups"`
	}
	e.expect(e.do("GET", "/api/chats/groups", member.Token, nil), 200).json(t, &mine)
	if len(mine.Groups) != 1 || mine.Groups[0].AvatarURL == nil || *mine.Groups[0].AvatarURL != out.AvatarURL {
		t.Fatalf("my groups: %s", e.do("GET", "/api/chats/groups", member.Token, nil).body)
	}
	var detail struct {
		AvatarURL *string `json:"avatar_url"`
	}
	if r := e.do("GET", "/api/chats/groups/"+g, outsider.Token, nil); r.code == 200 {
		r.json(t, &detail)
		if detail.AvatarURL != nil {
			t.Fatal("outsider sees the group picture link")
		}
	}
	var convs struct {
		Conversations []struct {
			ID        string  `json:"id"`
			AvatarURL *string `json:"avatar_url"`
		} `json:"conversations"`
	}
	e.expect(e.do("GET", "/api/chats", member.Token, nil), 200).json(t, &convs)
	if len(convs.Conversations) != 1 || convs.Conversations[0].AvatarURL == nil {
		t.Fatalf("conversations: %+v", convs)
	}

	e.expect(e.do("DELETE", path, member.Token, nil), 403)
	e.expect(e.do("DELETE", path, leader.Token, nil), 204)
	r, _ = e.doRaw("GET", path, member.Token, nil, nil)
	e.expect(r, 404)
}

func TestChangeEmail(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	start := func(tok string, body map[string]string, want int) resp {
		t.Helper()
		return e.expect(e.do("POST", "/api/users/me/email", tok, body), want)
	}
	start(alice.Token, map[string]string{"email": "new@example.io", "password": "wrong-Pass1!"}, 401)
	start(alice.Token, map[string]string{"email": "not an email", "password": password}, 400)
	start(alice.Token, map[string]string{"email": "ALICE@example.io", "password": password}, 400) // same

	// moving to bob's address: same answer, bob is told, nothing changes
	start(alice.Token, map[string]string{"email": "bobby@example.io", "password": password}, 202)
	if m, _ := e.mail.Last("bobby@example.io"); !strings.Contains(m.Subject, "tried to use your email") {
		t.Fatalf("bob got %q", m.Subject)
	}
	start(alice.Token, map[string]string{"email": "new@example.io", "password": password}, 429) // resend gap

	// a fresh code for a free address, after the gap
	e.now(`UPDATE email_codes SET created_at = created_at - interval '2 minutes' WHERE purpose = 'change_email' RETURNING 'ok'`, "ok")
	start(alice.Token, map[string]string{"email": " New@Example.io ", "password": password}, 202)
	code := e.lastCode("new@example.io")
	confirm := "/api/users/me/email/confirm"
	e.expect(e.do("POST", confirm, bob.Token, map[string]string{"code": code}), 400) // not bob's
	e.expect(e.do("POST", confirm, alice.Token, map[string]string{"code": "000000"}), 400)

	otherDevice := e.dial(alice.Token)
	var fresh user
	e.expect(e.do("POST", confirm, alice.Token, map[string]string{"code": code}), 200).json(t, &fresh)
	e.now(`SELECT email FROM users WHERE id = $1`, "new@example.io", alice.ID)
	if m, _ := e.mail.Last("alice@example.io"); !strings.Contains(m.Subject, "email address was changed") || strings.Contains(m.Text, "new@example.io") {
		t.Fatalf("old address got %q: %q", m.Subject, m.Text)
	}
	// old sessions end, the new one works, and the code is single-use
	if _, _, err := otherDevice.ReadMessage(); err == nil {
		t.Fatal("old websocket stayed open")
	}
	e.expect(e.do("GET", "/api/users/me", alice.Token, nil), 401)
	e.expect(e.do("POST", "/api/refresh", alice.RefreshToken, nil), 401)
	e.expect(e.do("GET", "/api/users/me", fresh.Token, nil), 200)
	e.expect(e.do("POST", confirm, fresh.Token, map[string]string{"code": code}), 400)
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": "new@example.io", "password": password}), 200)
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": "alice@example.io", "password": password}), 401)
}

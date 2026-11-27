//go:build integration

package integration

import (
	"testing"
)

func TestGroupNicknames(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol, out := e.signup("alice"), e.signup("bobby"), e.signup("carol"), e.signup("outsider")
	g := e.adminGroup("family", alice, bob, carol)
	e.device(carol, "tok-carol")
	nick := func(u user, v any, want int) {
		t.Helper()
		e.expect(e.do("PUT", "/api/chats/groups/"+g+"/nickname", u.Token, map[string]any{"nickname": v}), want)
	}
	nick(bob, " Big Bro ", 204)
	nick(bob, "this nickname is far too long for anyone", 400)
	nick(bob, "two\nlines", 400)
	nick(out, "Intruder", 400) // not a member
	var d struct {
		Members []struct {
			UserID   string  `json:"user_id"`
			Nickname *string `json:"nickname"`
		} `json:"members"`
	}
	e.expect(e.do("GET", "/api/chats/groups/"+g, alice.Token, nil), 200).json(t, &d)
	for _, m := range d.Members {
		if m.UserID == bob.ID && (m.Nickname == nil || *m.Nickname != "Big Bro") {
			t.Fatalf("bob's nickname %v", m.Nickname)
		}
		if m.UserID == alice.ID && m.Nickname != nil {
			t.Fatalf("alice has none: %v", *m.Nickname)
		}
	}
	// it's the name in the chat list preview and in the push
	e.send(bob, g, "public", "dinner at 7")
	if m := e.waitPush("tok-carol", 1)[0]; m.Title != "family" || m.Body != "Big Bro: dinner at 7" {
		t.Fatalf("push %+v", m)
	}
	var c struct {
		Conversations []struct {
			ID   string `json:"id"`
			Last *struct {
				FromName string `json:"from_name"`
			} `json:"last_message"`
		} `json:"conversations"`
	}
	e.expect(e.do("GET", "/api/chats", alice.Token, nil), 200).json(t, &c)
	for _, x := range c.Conversations {
		if x.ID == g && (x.Last == nil || x.Last.FromName != "Big Bro") {
			t.Fatalf("chat list %+v", x.Last)
		}
	}
	nick(bob, nil, 204) // cleared
	e.expect(e.do("GET", "/api/chats/groups/"+g, alice.Token, nil), 200).json(t, &d)
	for _, m := range d.Members {
		if m.UserID == bob.ID && m.Nickname != nil {
			t.Fatalf("still %v", *m.Nickname)
		}
	}
}

func TestMentions(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol, dave := e.signup("alice"), e.signup("bobby"), e.signup("carol"), e.signup("david")
	g := e.adminGroup("crew", alice, bob, carol, dave)
	e.befriend(alice, bob)
	for _, u := range []user{alice, carol, dave} {
		e.device(u, "tok-"+u.ID)
	}
	// carol muted the group; a mention still reaches her
	e.expect(e.do("PUT", "/api/mutes", carol.Token, map[string]string{"type": "public", "id": g}), 204)
	send := func(from user, to, kind string, body map[string]any, want int) {
		t.Helper()
		body["to_id"], body["type"] = to, kind
		e.expect(e.do("POST", "/api/chats/message", from.Token, body), want)
	}
	send(bob, g, "public", map[string]any{"msg": "@carol @carol you coming?", "mentions": []string{carol.ID, carol.ID}}, 200)
	if m := e.waitPush("tok-"+carol.ID, 1)[0]; m.Data["type"] != "mention" || m.Title != "crew" || m.Body != "bobby mentioned you: @carol @carol you coming?" {
		t.Fatalf("mention push %+v", m)
	}
	if m := e.waitPush("tok-"+alice.ID, 1)[0]; m.Data["type"] != "public" || m.Body != "bobby: @carol @carol you coming?" {
		t.Fatalf("others get the normal push: %+v", m)
	}
	// the mention is kept (once), in history
	var h struct {
		MsgList []struct {
			Info struct {
				Msg struct {
					Mentions   []string `json:"mentions"`
					MentionAll bool     `json:"mention_all"`
				}
			} `json:"message_info"`
		} `json:"msgList"`
	}
	e.expect(e.do("GET", "/api/chat/groups/"+g+"/messages", dave.Token, nil), 200).json(t, &h)
	if len(h.MsgList) != 1 || len(h.MsgList[0].Info.Msg.Mentions) != 1 || h.MsgList[0].Info.Msg.Mentions[0] != carol.ID {
		t.Fatalf("history %+v", h)
	}
	// only members; @everyone only for the admin; not in private chats
	send(bob, g, "public", map[string]any{"msg": "hi", "mentions": []string{e.signup("stranger").ID}}, 400)
	send(bob, g, "public", map[string]any{"msg": "everyone!", "mention_all": true}, 400)
	send(alice, bob.ID, "private", map[string]any{"msg": "hi", "mentions": []string{bob.ID}}, 400)
	send(alice, g, "public", map[string]any{"msg": "meeting at 5", "mention_all": true}, 200)
	if m := e.waitPush("tok-"+carol.ID, 2)[1]; m.Body != "alice mentioned everyone: meeting at 5" {
		t.Fatalf("@everyone push %+v", m)
	}
	e.waitPush("tok-"+dave.ID, 2)

	// "mentions" off: nothing for a muted group; older apps leave the switch alone
	var p map[string]any
	e.expect(e.do("PUT", "/api/users/me/notifications", carol.Token, map[string]bool{"private": true, "groups": true, "friends": true, "reactions": true, "mentions": false}), 200).json(t, &p)
	if p["mentions"] != false {
		t.Fatalf("prefs %v", p)
	}
	send(bob, g, "public", map[string]any{"msg": "carol?", "mentions": []string{carol.ID}}, 200)
	e.waitPush("tok-"+dave.ID, 3)
	e.waitPush("tok-"+carol.ID, 2)
	e.expect(e.do("PUT", "/api/users/me/notifications", carol.Token, map[string]bool{"private": true, "groups": true, "friends": true, "reactions": true}), 200).json(t, &p)
	if p["mentions"] != false {
		t.Fatalf("an older app turned mentions back on: %v", p)
	}
}

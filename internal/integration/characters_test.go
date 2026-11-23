//go:build integration

package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

type charInfo struct {
	Character  json.RawMessage `json:"character"`
	CharacterV *string         `json:"character_v"`
}

func (c charInfo) has(t *testing.T, where, body string) {
	t.Helper()
	if c.CharacterV == nil || !strings.Contains(string(c.Character), body) {
		t.Fatalf("%s: character %s v %v", where, c.Character, c.CharacterV)
	}
}

func TestCharacters(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.signup("alice"), e.signup("bobby"), e.signup("carol")
	e.befriend(alice, bob)

	var cat struct {
		Version int      `json:"version"`
		Bodies  []string `json:"bodies"`
	}
	e.expect(e.do("GET", "/api/characters/catalog", alice.Token, nil), 200).json(t, &cat)
	if cat.Version != 1 || len(cat.Bodies) == 0 || cat.Bodies[0] != "pigeon_round" {
		t.Fatalf("catalog %+v", cat)
	}

	// none yet: null, a normal state
	var me charInfo
	e.expect(e.do("GET", "/api/users/me", alice.Token, nil), 200).json(t, &me)
	if me.CharacterV != nil || string(me.Character) != "null" {
		t.Fatalf("before: %+v", me)
	}
	for _, bad := range []map[string]any{
		{"v": 1, "body": "human"},
		{"v": 1, "body": "pigeon_round", "parts": map[string]any{"hat": "tuft_01"}},
		{"v": 1, "body": "pigeon_round", "colors": map[string]any{"body": 99}},
		{"v": 2, "body": "pigeon_round"},
	} {
		e.expect(e.do("PUT", "/api/users/me/character", alice.Token, bad), 400)
	}
	var saved charInfo
	e.expect(e.do("PUT", "/api/users/me/character", alice.Token, map[string]any{
		"v": 1, "body": "pigeon_tall", "parts": map[string]any{"eyes": "happy_01", "hat": nil, "crest": "tuft_01"}, "colors": map[string]any{"body": 1, "wings": 5},
	}), 200).json(t, &saved)
	saved.has(t, "save", "pigeon_tall")
	first := *saved.CharacterV

	// it shows wherever alice appears
	e.expect(e.do("GET", "/api/users/me", alice.Token, nil), 200).json(t, &me)
	me.has(t, "/users/me", "pigeon_tall")
	var prof charInfo
	e.expect(e.do("GET", "/api/users/"+alice.ID, bob.Token, nil), 200).json(t, &prof)
	prof.has(t, "profile", "pigeon_tall")
	friendsOf := func(u user) []struct {
		UserID string `json:"user_id"`
		charInfo
	} {
		var f struct {
			Friends []struct {
				UserID string `json:"user_id"`
				charInfo
			} `json:"id_list"`
		}
		e.expect(e.do("GET", "/api/friends", u.Token, nil), 200).json(t, &f)
		return f.Friends
	}
	if f := friendsOf(bob); len(f) != 1 || f[0].UserID != alice.ID {
		t.Fatalf("friends %+v", f)
	} else {
		f[0].has(t, "friends", "pigeon_tall")
	}
	g := e.adminGroup("crew", alice, bob)
	var detail struct {
		Members []struct {
			UserID string `json:"user_id"`
			charInfo
		} `json:"members"`
	}
	e.expect(e.do("GET", "/api/chats/groups/"+g, bob.Token, nil), 200).json(t, &detail)
	for _, m := range detail.Members {
		if m.UserID == alice.ID {
			m.has(t, "group members", "pigeon_tall")
		}
	}
	e.send(alice, bob.ID, "private", "hi")
	var chats struct {
		Conversations []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			charInfo
		} `json:"conversations"`
	}
	e.expect(e.do("GET", "/api/chats", bob.Token, nil), 200).json(t, &chats)
	for _, c := range chats.Conversations {
		if c.Type == "private" && c.ID == alice.ID {
			c.has(t, "chat list", "pigeon_tall")
		}
	}
	e.expect(e.do("PUT", "/api/users/me/note", alice.Token, map[string]string{"text": "hello"}), 200)
	var notes struct {
		Notes []charInfo `json:"notes"`
	}
	e.expect(e.do("GET", "/api/notes", bob.Token, nil), 200).json(t, &notes)
	if len(notes.Notes) != 1 {
		t.Fatalf("notes %+v", notes)
	}
	notes.Notes[0].has(t, "notes", "pigeon_tall")

	// a new character gets a new version, and friends' cached lists follow
	e.expect(e.do("PUT", "/api/users/me/character", alice.Token, map[string]any{"v": 1, "body": "pigeon_round"}), 200).json(t, &saved)
	if *saved.CharacterV == first {
		t.Fatal("version unchanged")
	}
	if f := friendsOf(bob); !strings.Contains(string(f[0].Character), "pigeon_round") {
		t.Fatalf("friends list stale: %s", f[0].Character)
	}
	_ = carol
}

func TestAnimatedMessagesAndPigeonReactions(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.signup("alice"), e.signup("bobby"), e.signup("carol")
	e.befriend(alice, bob)
	g := e.adminGroup("crew", alice, bob, carol)
	e.device(bob, "tok-bob")

	send := func(from user, to, kind string, body map[string]any, want int) string {
		t.Helper()
		body["to_id"], body["type"] = to, kind
		var m struct {
			MsgID string `json:"msg_id"`
		}
		r := e.expect(e.do("POST", "/api/chats/message", from.Token, body), want)
		if want == 200 {
			r.json(t, &m)
		}
		return m.MsgID
	}
	// an animation alone, or with its fallback as text (older apps), or with text
	wave := send(alice, bob.ID, "private", map[string]any{"anim": "wave_01", "msg": ""}, 200)
	if m := e.waitPush("tok-bob", 1)[0]; m.Body != "👋 waved" {
		t.Fatalf("push %+v", m)
	}
	send(alice, bob.ID, "private", map[string]any{"anim": "fart_01", "msg": "💨 farted"}, 200)
	send(alice, bob.ID, "private", map[string]any{"anim": "dance_01", "msg": "party tonight?"}, 200)
	send(alice, bob.ID, "private", map[string]any{"anim": "moonwalk_99", "msg": ""}, 400)
	send(alice, bob.ID, "private", map[string]any{"msg": ""}, 400) // still no empty messages
	// a reply may carry an animation too; groups work the same
	send(bob, alice.ID, "private", map[string]any{"anim": "laugh_01", "msg": "", "parent_id": wave}, 200)
	gm := send(alice, g, "public", map[string]any{"anim": "party_01", "msg": ""}, 200)

	var h struct {
		MsgList []struct {
			Info struct {
				Msg struct {
					Content string `json:"msg"`
					Anim    string `json:"anim"`
				}
			} `json:"message_info"`
		} `json:"msgList"`
	}
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &h)
	if len(h.MsgList) != 4 || h.MsgList[0].Info.Msg.Anim != "wave_01" || h.MsgList[0].Info.Msg.Content != "" || h.MsgList[3].Info.Msg.Anim != "laugh_01" {
		t.Fatalf("history %+v", h)
	}

	// pigeon reactions: grouped apart from the plain emoji, with who reacted
	react := func(u user, msg string, body map[string]string, want int) {
		t.Helper()
		e.expect(e.do("PUT", "/api/chat/messages/"+msg+"/reaction", u.Token, body), want)
	}
	react(bob, gm, map[string]string{"emoji": "🤨", "anim": "sus_01"}, 204)
	react(carol, gm, map[string]string{"emoji": "🤨"}, 204)
	react(bob, gm, map[string]string{"emoji": "🤨", "anim": "nope_01"}, 400)
	type rc struct {
		Emoji   string   `json:"emoji"`
		Anim    string   `json:"anim"`
		Count   int      `json:"count"`
		UserIDs []string `json:"user_ids"`
	}
	var gh struct {
		MsgList []struct {
			Reactions []rc `json:"reactions"`
		} `json:"msgList"`
	}
	e.expect(e.do("GET", "/api/chat/groups/"+g+"/messages", alice.Token, nil), 200).json(t, &gh)
	rs := gh.MsgList[len(gh.MsgList)-1].Reactions
	if len(rs) != 2 || rs[0].Anim != "sus_01" || len(rs[0].UserIDs) != 1 || rs[0].UserIDs[0] != bob.ID || rs[1].Anim != "" || rs[1].UserIDs != nil {
		t.Fatalf("summary %+v", rs)
	}
	var who struct {
		Reactions []struct {
			UserID string `json:"user_id"`
			Anim   string `json:"anim"`
		} `json:"reactions"`
	}
	e.expect(e.do("GET", "/api/chat/messages/"+gm+"/reactions", alice.Token, nil), 200).json(t, &who)
	if len(who.Reactions) != 2 || who.Reactions[0].Anim != "sus_01" || who.Reactions[1].Anim != "" {
		t.Fatalf("reactors %+v", who)
	}
	// one reaction per person: a plain one replaces bob's pigeon one
	react(bob, gm, map[string]string{"emoji": "❤️"}, 204)
	gh.MsgList = nil // decode fresh: json keeps old fields the new answer leaves out
	e.expect(e.do("GET", "/api/chat/groups/"+g+"/messages", alice.Token, nil), 200).json(t, &gh)
	for _, r := range gh.MsgList[len(gh.MsgList)-1].Reactions {
		if r.Anim != "" {
			t.Fatalf("pigeon reaction left: %+v", r)
		}
	}
}

// High five and handshake: catalog animations, with their own push wording.
func TestTwoPigeonMoments(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.signup("alice"), e.signup("bobby"), e.signup("carol")
	e.befriend(alice, bob)
	g := e.adminGroup("crew", alice, bob, carol)
	e.device(bob, "tok-bob")
	e.device(alice, "tok-alice")
	e.device(carol, "tok-carol")
	msg := func(from user, to, kind string, body map[string]any) string {
		t.Helper()
		body["to_id"], body["type"] = to, kind
		var m struct {
			MsgID string `json:"msg_id"`
		}
		e.expect(e.do("POST", "/api/chats/message", from.Token, body), 200).json(t, &m)
		return m.MsgID
	}
	invite := msg(alice, bob.ID, "private", map[string]any{"anim": "highfive_ask_01", "msg": ""})
	msg(alice, bob.ID, "private", map[string]any{"anim": "highfive_ask_01", "msg": "great work today"})
	got := e.waitPush("tok-bob", 2)
	if got[0].Body != "alice wants a high five" || got[1].Body != "alice wants a high five: great work today" {
		t.Fatalf("invites: %q / %q", got[0].Body, got[1].Body)
	}
	msg(bob, alice.ID, "private", map[string]any{"anim": "highfive_01", "msg": "", "parent_id": invite})
	if m := e.waitPush("tok-alice", 1)[0]; m.Body != "bobby high-fived you back" || m.Data["type"] != "private" {
		t.Fatalf("answer %+v", m)
	}
	// as reactions, on the same message
	e.expect(e.do("PUT", "/api/chat/messages/"+invite+"/reaction", bob.Token, map[string]string{"emoji": "💀", "anim": "hanging_01"}), 204)
	if m := e.waitPush("tok-alice", 2)[1]; m.Body != "bobby left you hanging" || m.Data["type"] != "reaction" {
		t.Fatalf("reaction %+v", m)
	}
	// groups: the title is the group
	msg(alice, g, "public", map[string]any{"anim": "handshake_ask_01", "msg": ""})
	if m := e.waitPush("tok-carol", 1)[0]; m.Title != "crew" || m.Body != "alice wants to shake on it" {
		t.Fatalf("group %+v", m)
	}
	// every new id is accepted (the server refused them before)
	for _, id := range []string{"handshake_01"} {
		msg(bob, alice.ID, "private", map[string]any{"anim": id, "msg": ""})
	}
}

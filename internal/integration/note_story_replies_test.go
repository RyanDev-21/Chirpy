//go:build integration

package integration

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/websocket"
)

func TestNoteReplies(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	e.device(bob, "tok-bob")
	aliceWS := e.dial(alice.Token)

	// a friend's new note arrives live
	e.expect(e.do("PUT", "/api/users/me/note", bob.Token, map[string]string{"text": "at the gym"}), 200)
	if ev, p := nextOf(t, aliceWS, "Note"); p["user_id"] != bob.ID || p["text"] != "at the gym" || p["expires_at"] == nil {
		t.Fatalf("note event %s %v", ev, p)
	}

	send := func(body map[string]any, want int) {
		t.Helper()
		body["to_id"], body["type"] = bob.ID, "private"
		e.expect(e.do("POST", "/api/chats/message", alice.Token, body), want)
	}
	send(map[string]any{"msg": "nice!", "reply_to_note": true}, 200)
	send(map[string]any{"msg": "", "anim": "sus_01", "reply_to_note": true}, 200)
	send(map[string]any{"msg": "❤️", "reply_to_note": true}, 200)
	send(map[string]any{"msg": "hi", "note_text": "faked"}, 200) // clients can't set it
	got := e.waitPush("tok-bob", 4)
	for i, want := range []string{"replied to your note: nice!", "reacted to your note with their pigeon", "reacted ❤️ to your note", "hi"} {
		if got[i].Body != want {
			t.Fatalf("push %d: %q, want %q", i, got[i].Body, want)
		}
	}
	if got[0].Data["note"] != "true" || got[3].Data["note"] != "" {
		t.Fatalf("push data %v / %v", got[0].Data, got[3].Data)
	}

	// the reply keeps the note's text after the note is gone; new replies can't
	e.expect(e.do("DELETE", "/api/users/me/note", bob.Token, nil), 204)
	if _, p := nextOf(t, aliceWS, "Note"); p["text"] != "" || p["expires_at"] != nil {
		t.Fatalf("removed note event %v", p)
	}
	send(map[string]any{"msg": "still there?", "reply_to_note": true}, 400)
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"msg": "x", "to_id": "00000000-0000-0000-0000-000000000000", "type": "public", "reply_to_note": true}), 400)
	var h struct {
		MsgList []struct {
			Info struct {
				Msg struct {
					NoteText string `json:"note_text"`
				}
			} `json:"message_info"`
		} `json:"msgList"`
	}
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &h)
	if len(h.MsgList) != 4 || h.MsgList[0].Info.Msg.NoteText != "at the gym" || h.MsgList[2].Info.Msg.NoteText != "at the gym" || h.MsgList[3].Info.Msg.NoteText != "" {
		t.Fatalf("history %+v", h)
	}
}

func TestStoryReplyPushesAndReactions(t *testing.T) {
	e := newEnv(t)
	bob, alice, carol, dave := e.signup("bobby"), e.signup("alice"), e.signup("carol"), e.signup("david")
	for _, f := range []user{alice, carol, dave} {
		e.befriend(bob, f)
	}
	e.device(bob, "tok-bob")
	story, _ := e.postStory(bob)
	for _, u := range []user{alice, carol, dave} {
		e.expect(e.do("POST", "/api/stories/"+story+"/view", u.Token, nil), 204)
	}
	reply := func(u user, body map[string]any) {
		t.Helper()
		body["to_id"], body["type"], body["story_id"] = bob.ID, "private", story
		e.expect(e.do("POST", "/api/chats/message", u.Token, body), 200)
	}
	reply(alice, map[string]any{"msg": "", "anim": "sus_01"})
	reply(carol, map[string]any{"msg": "cool photo"})
	reply(dave, map[string]any{"msg": "😂"})
	got := e.waitPush("tok-bob", 3)
	for i, want := range []string{"reacted to your story with their pigeon", "replied to your story: cool photo", "reacted 😂 to your story"} {
		if got[i].Body != want || got[i].Data["story_id"] != story || got[i].Data["type"] != "private" {
			t.Fatalf("push %d: %+v, want %q", i, got[i], want)
		}
	}

	var v struct {
		Viewers []struct {
			UserID   string `json:"user_id"`
			Reaction *struct {
				Emoji string `json:"emoji"`
				Anim  string `json:"anim"`
			} `json:"reaction"`
		} `json:"viewers"`
	}
	e.expect(e.do("GET", "/api/stories/"+story+"/viewers", bob.Token, nil), 200).json(t, &v)
	by := map[string]*struct {
		Emoji string `json:"emoji"`
		Anim  string `json:"anim"`
	}{}
	for _, x := range v.Viewers {
		by[x.UserID] = x.Reaction
	}
	if r := by[alice.ID]; r == nil || r.Emoji != "🤨" || r.Anim != "sus_01" {
		t.Fatalf("alice %+v", r)
	}
	if by[carol.ID] != nil {
		t.Fatalf("a written reply isn't a reaction: %+v", by[carol.ID])
	}
	if r := by[dave.ID]; r == nil || r.Emoji != "😂" || r.Anim != "" {
		t.Fatalf("dave %+v", r)
	}
}

// nextOf reads events until one named name arrives.
func nextOf(t *testing.T, c *websocket.Conn, name string) (string, map[string]any) {
	t.Helper()
	for {
		ev := readEvent(t, c)
		if ev.Event == name {
			var p map[string]any
			_ = json.Unmarshal(ev.Payload, &p)
			return ev.Event, p
		}
	}
}

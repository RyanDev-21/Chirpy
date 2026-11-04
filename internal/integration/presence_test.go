//go:build integration

package integration

import (
	"encoding/json"
	"testing"
	"time"
)

func TestGroupReadBy(t *testing.T) {
	e := newEnv(t)
	leader, ann, ben, outsider := e.signup("leader"), e.signup("annie"), e.signup("benny"), e.signup("outsider")
	var g struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", leader.Token, map[string]any{"group_name": "crew", "max_mems": 5}), 201).json(t, &g)
	code := e.invite(leader.Token, g.ChatID)
	e.expect(e.join(ann.Token, code), 201)
	e.expect(e.join(ben.Token, code), 201)

	first := e.send(leader, g.ChatID, "public", "one")
	second := e.send(leader, g.ChatID, "public", "two")
	readBy := func(tok, msg string, want int) []struct {
		UserID string `json:"user_id"`
	} {
		t.Helper()
		var out struct {
			ReadBy []struct {
				UserID string `json:"user_id"`
			} `json:"read_by"`
		}
		e.expect(e.do("GET", "/api/chat/messages/"+msg+"/read-by", tok, nil), want).json(t, &out)
		return out.ReadBy
	}
	if got := readBy(leader.Token, second, 200); len(got) != 0 {
		t.Fatalf("nobody read yet, got %v", got)
	}

	leaderWS := e.dial(leader.Token)
	benWS := e.dial(ben.Token) // sent nothing: gets no read ticks
	mark := "/api/chat/groups/" + g.ChatID + "/messages"
	e.expect(e.do("POST", mark, ann.Token, map[string]string{"msg_id": second}), 201)
	e.expect(e.do("POST", mark, ben.Token, map[string]string{"msg_id": first}), 201)

	// live ticks for the sender
	for _, who := range []string{ann.ID, ben.ID} {
		ev := readEvent(t, leaderWS)
		var p struct {
			UserID, GroupID string
		}
		_ = json.Unmarshal(ev.Payload, &struct {
			UserID  *string `json:"user_id"`
			GroupID *string `json:"group_id"`
		}{&p.UserID, &p.GroupID})
		if ev.Event != "GroupRead" || p.UserID != who || p.GroupID != g.ChatID {
			t.Fatalf("got %s %+v, want GroupRead by %s", ev.Event, p, who)
		}
	}
	// marking an older message again moves nothing and sends nothing
	e.expect(e.do("POST", mark, ann.Token, map[string]string{"msg_id": first}), 201)
	// ben (not a sender) only got his own read echo, not ann's
	if ev := readEvent(t, benWS); ev.Event != "GroupRead" || !json.Valid(ev.Payload) || !containsID(ev.Payload, ben.ID) {
		t.Fatalf("ben got %s %s", ev.Event, ev.Payload)
	}
	e.send(ben, g.ChatID, "public", "three") // now ben is a sender
	_ = readEvent(t, leaderWS)               // the message
	_ = readEvent(t, benWS)                  // his own message echo
	third := e.send(leader, g.ChatID, "public", "four")
	_ = readEvent(t, benWS)
	e.expect(e.do("POST", mark, ann.Token, map[string]string{"msg_id": third}), 201)
	if ev := readEvent(t, benWS); ev.Event != "GroupRead" || !containsID(ev.Payload, ann.ID) {
		t.Fatalf("ben, now a sender, got %s %s", ev.Event, ev.Payload)
	}
	_ = readEvent(t, leaderWS) // "four" echo
	_ = readEvent(t, leaderWS) // ann's read

	if got := readBy(leader.Token, first, 200); len(got) != 2 {
		t.Fatalf("first read by %v, want ann and ben", got)
	}
	if got := readBy(leader.Token, third, 200); len(got) != 1 || got[0].UserID != ann.ID {
		t.Fatalf("second read by %v, want only ann", got)
	}

	// only the sender asks; outsiders can't tell the message exists
	readBy(ann.Token, second, 403)
	readBy(outsider.Token, second, 404)
	// private messages have no read-by
	e.befriend(leader, ann)
	priv := e.send(leader, ann.ID, "private", "hi")
	readBy(leader.Token, priv, 400)

	// a member who left no longer shows up
	e.expect(e.do("DELETE", "/api/chats/groups/"+g.ChatID+"/members", ben.Token, nil), 204)
	if got := readBy(leader.Token, first, 200); len(got) != 1 {
		t.Fatalf("after ben left: %v", got)
	}
}

type presenceList struct {
	Hide    bool `json:"hide_last_seen"`
	Friends []struct {
		UserID     string  `json:"user_id"`
		Online     bool    `json:"online"`
		LastSeenAt *string `json:"last_seen_at"`
		Hidden     bool    `json:"hidden"`
	} `json:"friends"`
}

func TestPresence(t *testing.T) {
	e := newEnv(t)
	alice, bob, stranger := e.signup("alice"), e.signup("bobby"), e.signup("stranger")
	e.befriend(alice, bob)

	aliceWS := e.dial(alice.Token)
	strangerWS := e.dial(stranger.Token)
	e.dial(bob.Token)
	ev := readAnyEvent(t, aliceWS)
	var p struct {
		UserID string `json:"user_id"`
		Online bool   `json:"online"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if ev.Event != "Presence" || p.UserID != bob.ID || !p.Online {
		t.Fatalf("alice got %s %+v", ev.Event, p)
	}

	var l presenceList
	e.expect(e.do("GET", "/api/friends/presence", alice.Token, nil), 200).json(t, &l)
	if l.Hide || len(l.Friends) != 1 || l.Friends[0].UserID != bob.ID || !l.Friends[0].Online {
		t.Fatalf("alice's view %+v", l)
	}
	e.expect(e.do("GET", "/api/friends/presence", stranger.Token, nil), 200).json(t, &l)
	if len(l.Friends) != 0 {
		t.Fatalf("stranger sees %+v", l)
	}

	// bob hides: alice is told, and neither sees the other
	e.expect(e.do("PUT", "/api/users/me/privacy", bob.Token, map[string]any{"hide_last_seen": "yes"}), 400)
	e.expect(e.do("PUT", "/api/users/me/privacy", bob.Token, map[string]any{"hide_last_seen": true}), 200)
	ev = readAnyEvent(t, aliceWS)
	var h struct {
		Online bool    `json:"online"`
		Hidden bool    `json:"hidden"`
		Last   *string `json:"last_seen_at"`
	}
	_ = json.Unmarshal(ev.Payload, &h)
	if ev.Event != "Presence" || !h.Hidden || h.Online || h.Last != nil {
		t.Fatalf("alice got %s %s", ev.Event, ev.Payload)
	}
	e.expect(e.do("GET", "/api/friends/presence", alice.Token, nil), 200).json(t, &l)
	if !l.Friends[0].Hidden || l.Friends[0].Online {
		t.Fatalf("alice still sees bob: %+v", l)
	}
	e.expect(e.do("GET", "/api/friends/presence", bob.Token, nil), 200).json(t, &l)
	if !l.Hide || !l.Friends[0].Hidden || l.Friends[0].Online {
		t.Fatalf("bob still sees alice: %+v", l)
	}
	var me struct {
		Hide bool `json:"hide_last_seen"`
	}
	e.expect(e.do("GET", "/api/users/me", bob.Token, nil), 200).json(t, &me)
	if !me.Hide {
		t.Fatal("/users/me should show hide_last_seen")
	}

	// the stranger never heard anything
	if err := strangerWS.SetReadDeadline(deadlineSoon()); err == nil {
		if _, raw, err := strangerWS.ReadMessage(); err == nil {
			t.Fatalf("stranger got %s", raw)
		}
	}
}

func deadlineSoon() time.Time { return time.Now().Add(300 * time.Millisecond) }

func containsID(raw []byte, id string) bool {
	var p struct {
		UserID string `json:"user_id"`
	}
	return json.Unmarshal(raw, &p) == nil && p.UserID == id
}

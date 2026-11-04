//go:build integration

package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

type reactionPage struct {
	MsgList []struct {
		ID        string `json:"message_id"`
		Reactions []struct {
			Emoji string `json:"emoji"`
			Count int    `json:"count"`
			Me    bool   `json:"me"`
		} `json:"reactions"`
	} `json:"msgList"`
}

func TestReactions(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.signup("alice"), e.signup("bob"), e.signup("carol")
	e.befriend(alice, bob)
	dm := e.send(alice, bob.ID, "private", "react to this")
	aliceWS := e.dial(alice.Token)
	reaction := "/api/chat/messages/" + dm + "/reaction"

	e.expect(e.do("PUT", reaction, bob.Token, map[string]string{"emoji": "nice"}), 400)
	e.expect(e.do("PUT", reaction, bob.Token, map[string]string{"emoji": "👍👍"}), 400)
	e.expect(e.do("PUT", reaction, carol.Token, map[string]string{"emoji": "👍"}), 404) // not her chat
	e.expect(e.do("PUT", "/api/chat/messages/00000000-0000-0000-0000-000000000000/reaction", bob.Token, map[string]string{"emoji": "👍"}), 404)

	e.expect(e.do("PUT", reaction, bob.Token, map[string]string{"emoji": "👍🏽"}), 204)
	ev := readEvent(t, aliceWS)
	var p struct {
		MsgID  string `json:"msg_id"`
		FromID string `json:"from_id"`
		Type   string `json:"type"`
		Emoji  string `json:"emoji"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if ev.Event != "Reaction" || p.MsgID != dm || p.FromID != bob.ID || p.Type != "private" || p.Emoji != "👍🏽" {
		t.Fatalf("alice got %s %+v", ev.Event, p)
	}
	e.expect(e.do("PUT", reaction, bob.Token, map[string]string{"emoji": "❤️"}), 204) // replaces
	e.expect(e.do("PUT", reaction, alice.Token, map[string]string{"emoji": "❤️"}), 204)

	var hist reactionPage
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &hist)
	r := hist.MsgList[0].Reactions
	if len(r) != 1 || r[0].Emoji != "❤️" || r[0].Count != 2 || !r[0].Me {
		t.Fatalf("bob sees %+v", r)
	}
	var who struct {
		Reactions []struct {
			UserID string `json:"user_id"`
			Name   string `json:"name"`
			Emoji  string `json:"emoji"`
		} `json:"reactions"`
	}
	e.expect(e.do("GET", "/api/chat/messages/"+dm+"/reactions", alice.Token, nil), 200).json(t, &who)
	if len(who.Reactions) != 2 || who.Reactions[0].Name != "bob" {
		t.Fatalf("reactors = %+v", who.Reactions)
	}
	e.expect(e.do("GET", "/api/chat/messages/"+dm+"/reactions", carol.Token, nil), 404)

	// removing: own reaction only, quiet when there is none
	e.expect(e.do("DELETE", reaction, bob.Token, nil), 204)
	e.expect(e.do("DELETE", reaction, bob.Token, nil), 204)
	e.expect(e.do("GET", "/api/chat/"+bob.ID+"/messages", alice.Token, nil), 200).json(t, &hist)
	if r := hist.MsgList[0].Reactions; len(r) != 1 || r[0].Count != 1 || !r[0].Me {
		t.Fatalf("after bob removed his: %+v", r)
	}

	// after unfriending you can remove a reaction but not add one
	e.expect(e.do("PUT", reaction, bob.Token, map[string]string{"emoji": "😂"}), 204)
	e.expect(e.do("DELETE", "/api/friends/"+alice.ID, bob.Token, nil), 204)
	e.expect(e.do("PUT", reaction, bob.Token, map[string]string{"emoji": "😮"}), 403)
	e.expect(e.do("DELETE", reaction, bob.Token, nil), 204)

	// groups: members react, outsiders can't see the message
	var g struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", alice.Token, map[string]any{"group_name": "reacts", "max_mems": 5}), 201).json(t, &g)
	e.expect(e.join(bob.Token, e.invite(alice.Token, g.ChatID)), 201)
	gm := e.send(alice, g.ChatID, "public", "group news")
	e.expect(e.do("PUT", "/api/chat/messages/"+gm+"/reaction", bob.Token, map[string]string{"emoji": "🎉"}), 204)
	e.expect(e.do("PUT", "/api/chat/messages/"+gm+"/reaction", carol.Token, map[string]string{"emoji": "🎉"}), 404)
	var gh reactionPage
	e.expect(e.do("GET", "/api/chat/groups/"+g.ChatID+"/messages", alice.Token, nil), 200).json(t, &gh)
	if r := gh.MsgList[0].Reactions; len(r) != 1 || r[0].Emoji != "🎉" || r[0].Me {
		t.Fatalf("group reactions for alice = %+v", r)
	}
	// a message without reactions has an empty list, never null
	e.send(alice, g.ChatID, "public", "no reactions here")
	raw := e.expect(e.do("GET", "/api/chat/groups/"+g.ChatID+"/messages", alice.Token, nil), 200)
	if !json.Valid(raw.body) || !strings.Contains(string(raw.body), `"reactions":[]`) {
		t.Fatalf("missing empty reactions list: %s", raw.body)
	}
}

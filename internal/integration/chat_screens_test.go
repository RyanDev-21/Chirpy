//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"
)

type page struct {
	MsgList []struct {
		ID   string `json:"message_id"`
		Info struct {
			Msg struct {
				Content string `json:"msg"`
			}
		} `json:"message_info"`
	} `json:"msgList"`
}

func (p page) texts() []string {
	out := make([]string, len(p.MsgList))
	for i, m := range p.MsgList {
		out[i] = m.Info.Msg.Content
	}
	return out
}

func (e *env) befriend(a, b user) {
	e.t.Helper()
	var r struct {
		ReqID string `json:"req_id"`
	}
	e.expect(e.do("POST", "/api/friends/requests", a.Token, map[string]string{"to_id": b.ID}), 200).json(e.t, &r)
	e.expect(e.do("PUT", "/api/friends/requests/"+r.ReqID, b.Token, map[string]string{"status": "confirm"}), 201)
}

func (e *env) send(from user, to, kind, text string) string {
	e.t.Helper()
	var m struct {
		MsgID string `json:"msg_id"`
	}
	e.expect(e.do("POST", "/api/chats/message", from.Token, map[string]string{"msg": text, "to_id": to, "type": kind}), 200).json(e.t, &m)
	return m.MsgID
}

func TestOlderHistoryPaging(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.signup("alice"), e.signup("bob"), e.signup("carol")
	e.befriend(alice, bob)
	e.befriend(alice, carol)
	for i := 1; i <= 7; i++ {
		e.send(alice, bob.ID, "private", fmt.Sprintf("m%d", i))
	}
	other := e.send(alice, carol.ID, "private", "elsewhere")

	base := "/api/chat/" + alice.ID + "/messages"
	var p page
	e.expect(e.do("GET", base+"?limit=3", bob.Token, nil), 200).json(t, &p)
	if got := fmt.Sprint(p.texts()); got != "[m5 m6 m7]" {
		t.Fatalf("latest page = %s", got)
	}
	var seen []string
	cursor := p.MsgList[0].ID
	for {
		var older page
		e.expect(e.do("GET", base+"?limit=3&before="+cursor, bob.Token, nil), 200).json(t, &older)
		if len(older.MsgList) == 0 {
			break
		}
		seen = append(older.texts(), seen...)
		cursor = older.MsgList[0].ID
	}
	if got := fmt.Sprint(seen); got != "[m1 m2 m3 m4]" {
		t.Fatalf("paged back = %s", got)
	}
	// a cursor from another conversation, or garbage, is rejected
	e.expect(e.do("GET", base+"?before="+other, bob.Token, nil), 400)
	e.expect(e.do("GET", base+"?before=nope", bob.Token, nil), 400)
	e.expect(e.do("GET", base+"?before="+cursor+"&since=2026-01-01T00:00:00Z", bob.Token, nil), 400)

	// groups page the same way
	var g struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", alice.Token, map[string]any{"group_name": "pages", "max_mems": 5}), 201).json(t, &g)
	for i := 1; i <= 4; i++ {
		e.send(alice, g.ChatID, "public", fmt.Sprintf("g%d", i))
	}
	gbase := "/api/chat/groups/" + g.ChatID + "/messages"
	e.expect(e.do("GET", gbase+"?limit=2", alice.Token, nil), 200).json(t, &p)
	var gp page
	e.expect(e.do("GET", gbase+"?limit=2&before="+p.MsgList[0].ID, alice.Token, nil), 200).json(t, &gp)
	if got := fmt.Sprint(gp.texts()); got != "[g1 g2]" {
		t.Fatalf("group older page = %s", got)
	}
	e.expect(e.do("GET", gbase+"?before="+p.MsgList[0].ID, bob.Token, nil), 403)
}

type convList struct {
	Conversations []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Last *struct {
			Msg    string `json:"msg"`
			FromID string `json:"from_id"`
		} `json:"last_message"`
		Unread int `json:"unread"`
	} `json:"conversations"`
}

func (e *env) conversations(u user) convList {
	e.t.Helper()
	var c convList
	e.expect(e.do("GET", "/api/chats", u.Token, nil), 200).json(e.t, &c)
	return c
}

// waitUnread polls until the conversation `id` shows `want` unread (read
// receipts are written by a background job).
func (e *env) waitUnread(u user, id string, want int) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	got := -1
	for time.Now().Before(deadline) {
		for _, c := range e.conversations(u).Conversations {
			if c.ID == id {
				got = c.Unread
			}
		}
		if got == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatalf("unread for %s = %d, want %d", id, got, want)
}

func TestConversationsAndUnread(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.signup("alice"), e.signup("bob"), e.signup("carol")
	e.befriend(alice, bob)
	e.befriend(carol, bob)

	var quiet, busy struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", bob.Token, map[string]any{"group_name": "quiet", "max_mems": 5}), 201).json(t, &quiet)
	e.expect(e.do("POST", "/api/chats/groups", bob.Token, map[string]any{"group_name": "busy", "max_mems": 5}), 201).json(t, &busy)
	e.expect(e.join(alice.Token, e.invite(bob.Token, busy.ChatID)), 201)

	e.send(alice, bob.ID, "private", "hi bob")
	last := e.send(alice, bob.ID, "private", "you there?")
	e.send(carol, bob.ID, "private", "from carol")
	g1 := e.send(alice, busy.ChatID, "public", "standup")
	e.send(alice, busy.ChatID, "public", "notes")
	e.send(bob, busy.ChatID, "public", "mine") // own messages never count as unread

	c := e.conversations(bob)
	if len(c.Conversations) != 4 {
		t.Fatalf("bob's conversations = %+v", c.Conversations)
	}
	// most recent activity first; the group without messages last
	order := fmt.Sprint(c.Conversations[0].Name, ",", c.Conversations[1].Name, ",", c.Conversations[2].Name, ",", c.Conversations[3].Name)
	if order != "busy,carol,alice,quiet" {
		t.Fatalf("order = %s", order)
	}
	byName := map[string]int{}
	for _, cv := range c.Conversations {
		byName[cv.Name] = cv.Unread
	}
	if byName["alice"] != 2 || byName["carol"] != 1 || byName["busy"] != 2 || byName["quiet"] != 0 {
		t.Fatalf("unread = %v", byName)
	}
	if c.Conversations[3].Last != nil || c.Conversations[0].Last.Msg != "mine" {
		t.Fatalf("last messages = %+v / %+v", c.Conversations[0].Last, c.Conversations[3].Last)
	}

	// reading clears unread: private via the seen receipt, group via a read marker
	e.expect(e.do("POST", "/api/chat/"+alice.ID+"/messages", bob.Token, map[string]string{"msg_id": last}), 201)
	e.waitUnread(bob, alice.ID, 0)
	e.expect(e.do("POST", "/api/chat/groups/"+busy.ChatID+"/messages", bob.Token, map[string]string{"msg_id": g1}), 201)
	e.waitUnread(bob, busy.ChatID, 1)
	e.send(alice, busy.ChatID, "public", "one more")
	e.waitUnread(bob, busy.ChatID, 2)

	// read markers never move backwards; foreign ids and non-members are refused
	latest := e.send(alice, busy.ChatID, "public", "latest")
	e.expect(e.do("POST", "/api/chat/groups/"+busy.ChatID+"/messages", bob.Token, map[string]string{"msg_id": latest}), 201)
	e.expect(e.do("POST", "/api/chat/groups/"+busy.ChatID+"/messages", bob.Token, map[string]string{"msg_id": g1}), 201)
	e.waitUnread(bob, busy.ChatID, 0)
	e.expect(e.do("POST", "/api/chat/groups/"+busy.ChatID+"/messages", bob.Token, map[string]string{"msg_id": last}), 400)
	e.expect(e.do("POST", "/api/chat/groups/"+busy.ChatID+"/messages", carol.Token, map[string]string{"msg_id": latest}), 403)
}

func TestUnfriend(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bob")
	e.befriend(alice, bob)
	e.send(alice, bob.ID, "private", "before")
	bobWS := e.dial(bob.Token)

	e.expect(e.do("DELETE", "/api/friends/"+bob.ID, alice.Token, nil), 204)
	if ev := readEvent(t, bobWS); ev.Event != "RemoveFri" {
		t.Fatalf("bob got %q, want RemoveFri", ev.Event)
	}
	e.expect(e.do("DELETE", "/api/friends/"+bob.ID, alice.Token, nil), 404)

	var fl struct {
		List []struct{} `json:"id_list"`
	}
	e.expect(e.do("GET", "/api/friends", bob.Token, nil), 200).json(t, &fl)
	if len(fl.List) != 0 {
		t.Fatalf("still in bob's friend list: %+v", fl.List)
	}
	e.expect(e.do("POST", "/api/chats/message", bob.Token, map[string]string{"msg": "hey", "to_id": alice.ID, "type": "private"}), 403)
	var p page
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &p)
	if len(p.MsgList) != 1 {
		t.Fatalf("history should stay readable: %+v", p.texts())
	}
	// and they can start over
	e.befriend(bob, alice)
}

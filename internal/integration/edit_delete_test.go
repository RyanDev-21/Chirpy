//go:build integration

package integration

import (
	"encoding/json"
	"testing"
	"time"
)

type editPage struct {
	MsgList []struct {
		ID      string `json:"message_id"`
		MsgInfo struct {
			Msg struct {
				Content  string `json:"msg"`
				ParentID string `json:"parent_id"`
			} `json:"Msg"`
		} `json:"message_info"`
		EditedAt  *time.Time `json:"edited_at"`
		Deleted   bool       `json:"deleted"`
		Reactions []struct {
			Emoji string `json:"emoji"`
		} `json:"reactions"`
	} `json:"msgList"`
}

func TestEditAndDeletePrivateMessage(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bob")
	e.befriend(alice, bob)
	dm := e.send(alice, bob.ID, "private", "helo")
	path := "/api/chat/messages/" + dm
	bobWS := e.dial(bob.Token)

	e.expect(e.do("PATCH", path, bob.Token, map[string]string{"msg": "mine now"}), 403)
	e.expect(e.do("PATCH", path, alice.Token, map[string]string{"msg": "   "}), 400)
	var edited struct {
		Content  string    `json:"msg"`
		EditedAt time.Time `json:"edited_at"`
	}
	e.expect(e.do("PATCH", path, alice.Token, map[string]string{"msg": "hello"}), 200).json(t, &edited)
	if edited.Content != "hello" || edited.EditedAt.IsZero() {
		t.Fatalf("edit response %+v", edited)
	}
	ev := readEvent(t, bobWS)
	var p struct {
		MsgID   string `json:"msg_id"`
		FromID  string `json:"from_id"`
		Content string `json:"msg"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if ev.Event != "MsgEdited" || p.MsgID != dm || p.FromID != alice.ID || p.Content != "hello" {
		t.Fatalf("bob got %s %+v", ev.Event, p)
	}
	var page editPage
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &page)
	if m := page.MsgList[0]; m.MsgInfo.Msg.Content != "hello" || m.EditedAt == nil || m.Deleted {
		t.Fatalf("history after edit %+v", m)
	}

	// edits close 15 minutes after sending
	if _, err := e.db.Exec(`UPDATE message SET created_at = created_at - interval '16 minutes' WHERE id = $1`, dm); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("PATCH", path, alice.Token, map[string]string{"msg": "too late"}), 403)

	// a reply, and a reaction that the delete removes
	reply := e.do("POST", "/api/chats/message", bob.Token, map[string]string{"msg": "re", "to_id": alice.ID, "type": "private", "parent_id": dm})
	e.expect(reply, 200)
	e.expect(e.do("PUT", path+"/reaction", bob.Token, map[string]string{"emoji": "👍"}), 204)
	e.expect(e.do("DELETE", path, bob.Token, nil), 403)
	e.expect(e.do("DELETE", path, alice.Token, nil), 204)
	e.expect(e.do("DELETE", path, alice.Token, nil), 404)
	e.expect(e.do("PUT", path+"/reaction", bob.Token, map[string]string{"emoji": "👍"}), 404)

	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &page)
	if len(page.MsgList) != 2 {
		t.Fatalf("want placeholder + reply, got %+v", page.MsgList)
	}
	if m := page.MsgList[0]; !m.Deleted || m.MsgInfo.Msg.Content != "" || len(m.Reactions) != 0 {
		t.Fatalf("placeholder %+v", m)
	}
	if page.MsgList[1].MsgInfo.Msg.ParentID != dm {
		t.Fatalf("reply lost its parent: %+v", page.MsgList[1])
	}
	e.now(`SELECT coalesce(content, 'NULL') FROM message WHERE id = $1`, "NULL", dm)
	e.now(`SELECT count(*)::text FROM message_reactions WHERE message_id = $1`, "0", dm)
}

func TestDeleteWorksAfterUnfriendButEditDoesNot(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bob")
	e.befriend(alice, bob)
	dm := e.send(alice, bob.ID, "private", "regret")
	e.expect(e.do("DELETE", "/api/friends/"+bob.ID, alice.Token, nil), 204)
	e.expect(e.do("PATCH", "/api/chat/messages/"+dm, alice.Token, map[string]string{"msg": "x"}), 403)
	e.expect(e.do("DELETE", "/api/chat/messages/"+dm, alice.Token, nil), 204)

	c := e.conversations(alice)
	if len(c.Conversations) != 1 || c.Conversations[0].Last == nil || c.Conversations[0].Last.Msg != "" {
		t.Fatalf("conversation list %+v", c)
	}
}

func TestEditAndDeleteGroupMessage(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.signup("alice"), e.signup("bob"), e.signup("carol")
	var g struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", alice.Token, map[string]any{"group_name": "edits", "max_mems": 5}), 201).json(t, &g)
	e.expect(e.join(bob.Token, e.invite(alice.Token, g.ChatID)), 201)
	gm := e.send(alice, g.ChatID, "public", "standup at 10")
	bobWS := e.dial(bob.Token)
	path := "/api/chat/messages/" + gm

	e.expect(e.do("PATCH", path, carol.Token, map[string]string{"msg": "x"}), 404) // not a member
	e.expect(e.do("PATCH", path, bob.Token, map[string]string{"msg": "x"}), 403)
	e.expect(e.do("PATCH", path, alice.Token, map[string]string{"msg": "standup at 11"}), 200)
	ev := readEvent(t, bobWS)
	var p struct {
		GroupID string `json:"group_id"`
		Content string `json:"msg"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if ev.Event != "MsgEdited" || p.GroupID != g.ChatID || p.Content != "standup at 11" {
		t.Fatalf("bob got %s %+v", ev.Event, p)
	}
	e.expect(e.do("DELETE", path, alice.Token, nil), 204)
	if ev := readEvent(t, bobWS); ev.Event != "MsgDeleted" {
		t.Fatalf("bob got %s", ev.Event)
	}
	var page editPage
	e.expect(e.do("GET", "/api/chat/groups/"+g.ChatID+"/messages", bob.Token, nil), 200).json(t, &page)
	if len(page.MsgList) != 1 || !page.MsgList[0].Deleted || page.MsgList[0].MsgInfo.Msg.Content != "" {
		t.Fatalf("group history %+v", page.MsgList)
	}
}

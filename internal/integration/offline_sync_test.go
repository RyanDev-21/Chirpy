//go:build integration

package integration

import (
	"context"
	"net/url"
	"testing"
	"time"
)

// A phone that lost the first answer retries hours later, after a deploy:
// the server still knows the client_msg_id and returns the same message.
func TestRetryAfterRestartIsNotDuplicated(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	g := e.adminGroup("crew", alice, bob)

	type sent struct {
		MsgID string `json:"msg_id"`
	}
	for _, c := range []struct{ kind, to, client string }{
		{"private", bob.ID, "6a1e0c5f-8a2b-4c3d-9e4f-0a1b2c3d4e5f"},
		{"public", g, "7b2f1d60-8a2b-4c3d-9e4f-0a1b2c3d4e5f"},
	} {
		body := map[string]string{"msg": "only once", "to_id": c.to, "type": c.kind, "client_msg_id": c.client}
		var first, again sent
		e.expect(e.do("POST", "/api/chats/message", alice.Token, body), 200).json(t, &first)
		e.restart() // the in-memory list is gone
		e.expect(e.do("POST", "/api/chats/message", alice.Token, body), 200).json(t, &again)
		if first.MsgID == "" || again.MsgID != first.MsgID {
			t.Fatalf("%s: retry after restart got %q, first %q", c.kind, again.MsgID, first.MsgID)
		}
		// the same id from someone else is their own message
		var other sent
		e.expect(e.do("POST", "/api/chats/message", bob.Token, map[string]string{"msg": "mine", "to_id": map[string]string{"private": alice.ID, "public": g}[c.kind], "type": c.kind, "client_msg_id": c.client}), 200).json(t, &other)
		if other.MsgID == first.MsgID {
			t.Fatalf("%s: bob's send was taken for alice's", c.kind)
		}
	}
	var p page
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &p)
	if len(p.MsgList) != 2 {
		t.Fatalf("private history %v", p.texts())
	}
	p = page{}
	e.expect(e.do("GET", "/api/chat/groups/"+g+"/messages", bob.Token, nil), 200).json(t, &p)
	if len(p.MsgList) != 2 {
		t.Fatalf("group history %v", p.texts())
	}

	// after 7 days the id is forgotten (the janitor deletes it)
	if _, err := e.db.Exec(`UPDATE sent_ids SET created_at = created_at - interval '8 days'`); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.now(`SELECT count(*)::text FROM sent_ids`, "0")
}

type changeList struct {
	Changes []struct {
		Type    string `json:"type"`
		ChatID  string `json:"chat_id"`
		Message struct {
			ID        string  `json:"message_id"`
			EditedAt  *string `json:"edited_at"`
			ChangedAt string  `json:"changed_at"`
			Deleted   bool    `json:"deleted"`
			Info      struct {
				Msg struct {
					Content string `json:"msg"`
				}
			} `json:"message_info"`
			Reactions []struct {
				Emoji string `json:"emoji"`
				Count int    `json:"count"`
			} `json:"reactions"`
		} `json:"message"`
	} `json:"changes"`
	More bool `json:"more"`
}

func (e *env) changes(u user, since string, want int) changeList {
	e.t.Helper()
	var c changeList
	e.expect(e.do("GET", "/api/chats/changes?since="+url.QueryEscape(since), u.Token, nil), want).json(e.t, &c)
	return c
}

// Edits, deletes and reactions made while a phone was offline come back
// from /api/chats/changes, for every chat at once.
func TestChangesWhileOffline(t *testing.T) {
	e := newEnv(t)
	alice, bob, ann, late := e.signup("alice"), e.signup("bobby"), e.signup("annie"), e.signup("later")
	e.befriend(alice, bob)
	g := e.adminGroup("crew", alice, ann)
	toEdit := e.send(bob, alice.ID, "private", "typo")
	toDelete := e.send(alice, bob.ID, "private", "oops")
	toReact := e.send(alice, g, "public", "trail?")
	untouched := e.send(ann, g, "public", "hi")
	_ = untouched

	since := time.Now().UTC().Format(time.RFC3339Nano)
	if c := e.changes(alice, since, 200); len(c.Changes) != 0 || c.More {
		t.Fatalf("nothing changed yet: %+v", c)
	}
	time.Sleep(5 * time.Millisecond)
	e.expect(e.do("PATCH", "/api/chat/messages/"+toEdit, bob.Token, map[string]string{"msg": "fixed"}), 200)
	e.expect(e.do("DELETE", "/api/chat/messages/"+toDelete, alice.Token, nil), 204)
	e.expect(e.do("PUT", "/api/chat/messages/"+toReact+"/reaction", ann.Token, map[string]string{"emoji": "🎉"}), 204)

	c := e.changes(alice, since, 200)
	if len(c.Changes) != 3 || c.More {
		t.Fatalf("alice's changes %+v", c)
	}
	got := map[string]int{}
	for i, ch := range c.Changes {
		got[ch.Message.ID] = i
		if i > 0 && ch.Message.ChangedAt < c.Changes[i-1].Message.ChangedAt {
			t.Fatalf("not oldest change first: %+v", c.Changes)
		}
	}
	ed, del, re := c.Changes[got[toEdit]], c.Changes[got[toDelete]], c.Changes[got[toReact]]
	if ed.Type != "private" || ed.ChatID != bob.ID || ed.Message.Info.Msg.Content != "fixed" || ed.Message.EditedAt == nil {
		t.Fatalf("edit %+v", ed)
	}
	if del.Type != "private" || del.ChatID != bob.ID || !del.Message.Deleted || del.Message.Info.Msg.Content != "" {
		t.Fatalf("delete %+v", del)
	}
	if re.Type != "public" || re.ChatID != g || len(re.Message.Reactions) != 1 || re.Message.Reactions[0].Emoji != "🎉" {
		t.Fatalf("reaction %+v", re)
	}

	// removing the reaction is a change too; asking from the last change on
	// returns only that
	last := c.Changes[2].Message.ChangedAt
	time.Sleep(5 * time.Millisecond)
	e.expect(e.do("DELETE", "/api/chat/messages/"+toReact+"/reaction", ann.Token, nil), 204)
	if c = e.changes(alice, last, 200); len(c.Changes) != 1 || c.Changes[0].Message.ID != toReact || len(c.Changes[0].Message.Reactions) != 0 {
		t.Fatalf("after unreact %+v", c)
	}

	// only your own chats; groups only since you joined
	if c = e.changes(ann, since, 200); len(c.Changes) != 1 || c.Changes[0].Message.ID != toReact {
		t.Fatalf("ann sees %+v", c)
	}
	e.expect(e.join(late.Token, e.invite(alice.Token, g)), 201)
	if c = e.changes(late, since, 200); len(c.Changes) != 0 {
		t.Fatalf("a later member sees older changes %+v", c)
	}
	e.changes(alice, "", 400)
	e.changes(alice, "yesterday", 400)
}

// Paging: more is set when there are further changes; asking again from
// the last changed_at continues without gaps.
func TestChangesPaging(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	since := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	if _, err := e.db.Exec(`
		INSERT INTO message (id, content, from_id, to_id, created_at, changed_at)
		SELECT gen_random_uuid(), 'm' || g, $1, $2, now() AT TIME ZONE 'UTC' - interval '1 hour',
		       now() AT TIME ZONE 'UTC' + g * interval '1 millisecond'
		FROM generate_series(1, 250) g`, bob.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	c := e.changes(alice, since, 200)
	if len(c.Changes) != 200 || !c.More {
		t.Fatalf("page 1: %d changes, more %v", len(c.Changes), c.More)
	}
	c2 := e.changes(alice, c.Changes[199].Message.ChangedAt, 200)
	if len(c2.Changes) != 50 || c2.More || c2.Changes[0].Message.Info.Msg.Content != "m201" {
		t.Fatalf("page 2: %d changes, more %v", len(c2.Changes), c2.More)
	}
}

//go:build integration

package integration

import (
	"encoding/json"
	"testing"
)

// adminGroup creates a group led by leader with the given members.
func (e *env) adminGroup(name string, leader user, members ...user) string {
	e.t.Helper()
	var g struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", leader.Token, map[string]any{"group_name": name, "max_mems": 10}), 201).json(e.t, &g)
	code := e.invite(leader.Token, g.ChatID)
	for _, m := range members {
		e.expect(e.join(m.Token, code), 201)
	}
	return g.ChatID
}

func groupEvent(t *testing.T, ev wsEvent) (change, userID, reason string) {
	t.Helper()
	var p struct {
		Change string `json:"change"`
		UserID string `json:"user_id"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	return p.Change, p.UserID, p.Reason
}

func TestKickMember(t *testing.T) {
	e := newEnv(t)
	lead, bob, carol := e.signup("lead"), e.signup("bob"), e.signup("carol")
	g := e.adminGroup("kicks", lead, bob, carol)
	bobWS, carolWS := e.dial(bob.Token), e.dial(carol.Token)
	base := "/api/chats/groups/" + g + "/members/"

	e.expect(e.do("DELETE", base+carol.ID, bob.Token, nil), 403) // not the leader
	e.expect(e.do("DELETE", base+lead.ID, lead.Token, nil), 400) // yourself
	e.expect(e.do("DELETE", base+lead.ID, e.signup("dave").Token, nil), 404)
	e.expect(e.do("DELETE", base+bob.ID, lead.Token, nil), 204)
	e.expect(e.do("DELETE", base+bob.ID, lead.Token, nil), 400) // not a member any more

	if ev := readEvent(t, bobWS); ev.Event != "GroupRemoved" {
		t.Fatalf("bob got %s", ev.Event)
	} else if _, _, reason := groupEvent(t, ev); reason != "kicked" {
		t.Fatalf("reason %q", reason)
	}
	if ev := readEvent(t, carolWS); ev.Event != "GroupUpdated" {
		t.Fatalf("carol got %s", ev.Event)
	} else if change, uid, _ := groupEvent(t, ev); change != "members" || uid != bob.ID {
		t.Fatalf("carol got %s %s", change, uid)
	}
	// bob lost access; the seat is free
	e.expect(e.do("GET", "/api/chat/groups/"+g+"/messages", bob.Token, nil), 403)
	e.expect(e.do("POST", "/api/chats/message", bob.Token, map[string]string{"msg": "hi", "to_id": g, "type": "public"}), 403)
	e.now(`SELECT current_member::text FROM chat_groups WHERE id = $1`, "2", g)
}

func TestRenameAndDescribeGroup(t *testing.T) {
	e := newEnv(t)
	lead, bob := e.signup("lead"), e.signup("bob")
	g := e.adminGroup("before", lead, bob)
	e.adminGroup("Taken", e.signup("other"))
	bobWS := e.dial(bob.Token)
	path := "/api/chats/groups/" + g

	e.expect(e.do("PATCH", path, bob.Token, map[string]string{"group_name": "mine"}), 403)
	e.expect(e.do("PATCH", path, lead.Token, map[string]string{}), 400)
	e.expect(e.do("PATCH", path, lead.Token, map[string]string{"group_name": " "}), 400)
	e.expect(e.do("PATCH", path, lead.Token, map[string]string{"group_name": "taken"}), 200) // names needn't be unique
	_ = readEvent(t, bobWS)
	var out struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	e.expect(e.do("PATCH", path, lead.Token, map[string]string{"group_name": " after ", "description": "new"}), 200).json(t, &out)
	if out.Name != "after" || out.Description != "new" {
		t.Fatalf("update response %+v", out)
	}
	e.expect(e.do("PATCH", path, lead.Token, map[string]string{"description": "only this"}), 200).json(t, &out)
	if out.Name != "after" || out.Description != "only this" {
		t.Fatalf("partial update %+v", out)
	}
	if ev := readEvent(t, bobWS); ev.Event != "GroupUpdated" {
		t.Fatalf("bob got %s", ev.Event)
	} else if change, _, _ := groupEvent(t, ev); change != "info" {
		t.Fatalf("change %q", change)
	}
}

func TestTransferLeadership(t *testing.T) {
	e := newEnv(t)
	lead, bob := e.signup("lead"), e.signup("bob")
	g := e.adminGroup("handover", lead, bob)
	bobWS := e.dial(bob.Token)
	path := "/api/chats/groups/" + g + "/admin"

	e.expect(e.do("PUT", path, bob.Token, map[string]string{"user_id": bob.ID}), 403)
	e.expect(e.do("PUT", path, lead.Token, map[string]string{"user_id": lead.ID}), 400)
	e.expect(e.do("PUT", path, lead.Token, map[string]string{"user_id": e.signup("dave").ID}), 400) // not a member
	e.expect(e.do("PUT", path, lead.Token, map[string]string{"user_id": bob.ID}), 204)
	if ev := readEvent(t, bobWS); ev.Event != "GroupUpdated" {
		t.Fatalf("bob got %s", ev.Event)
	} else if change, uid, _ := groupEvent(t, ev); change != "admin" || uid != bob.ID {
		t.Fatalf("bob got %s %s", change, uid)
	}
	e.now(`SELECT role FROM member_table WHERE group_id = $1 AND member_id = $2`, "admin", g, bob.ID)
	e.now(`SELECT role FROM member_table WHERE group_id = $1 AND member_id = $2`, "member", g, lead.ID)
	// the old admin is a member now; the new one can regenerate the invite
	e.expect(e.do("POST", "/api/chats/groups/"+g+"/invite", lead.Token, nil), 403)
	e.expect(e.do("POST", "/api/chats/groups/"+g+"/invite", bob.Token, nil), 200)
	e.expect(e.do("DELETE", "/api/chats/groups/"+g+"/members", lead.Token, nil), 204)
}

func TestDeleteGroup(t *testing.T) {
	e := newEnv(t)
	lead, bob := e.signup("lead"), e.signup("bob")
	g := e.adminGroup("doomed", lead, bob)
	m1 := e.send(lead, g, "public", "first")
	reply := e.do("POST", "/api/chats/message", bob.Token, map[string]string{"msg": "re", "to_id": g, "type": "public", "parent_id": m1})
	e.expect(reply, 200)
	e.expect(e.do("PUT", "/api/chat/messages/"+m1+"/reaction", bob.Token, map[string]string{"emoji": "👍"}), 204)
	e.expect(e.do("GET", "/api/chat/groups/"+g+"/messages", bob.Token, nil), 200) // warm the cache
	bobWS := e.dial(bob.Token)

	e.expect(e.do("DELETE", "/api/chats/groups/"+g, bob.Token, nil), 403)
	e.expect(e.do("DELETE", "/api/chats/groups/"+g, lead.Token, nil), 204)
	if ev := readEvent(t, bobWS); ev.Event != "GroupRemoved" {
		t.Fatalf("bob got %s", ev.Event)
	} else if _, _, reason := groupEvent(t, ev); reason != "deleted" {
		t.Fatalf("reason %q", reason)
	}
	e.expect(e.do("GET", "/api/chats/groups/"+g, lead.Token, nil), 404)
	e.expect(e.do("DELETE", "/api/chats/groups/"+g, lead.Token, nil), 404)
	for _, q := range []string{
		`SELECT count(*)::text FROM GroupMessage WHERE group_id = $1`,
		`SELECT count(*)::text FROM member_table WHERE group_id = $1`,
	} {
		e.now(q, "0", g)
	}
	e.now(`SELECT count(*)::text FROM group_message_reactions`, "0")
	if n, _ := e.redis.Exists(t.Context(), "chat:group:"+g+":msgs").Result(); n != 0 {
		t.Fatal("deleted group's messages are still cached")
	}
	c := e.conversations(bob)
	if len(c.Conversations) != 0 {
		t.Fatalf("bob still lists %+v", c)
	}
}

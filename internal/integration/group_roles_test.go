//go:build integration

package integration

import (
	"strings"
	"testing"
)

func (e *env) role(g, admin, target string, role string, perms ...string) resp {
	e.t.Helper()
	if perms == nil {
		perms = []string{}
	}
	return e.do("PUT", "/api/chats/groups/"+g+"/members/"+target+"/role", admin, map[string]any{"role": role, "permissions": perms})
}

func TestModerators(t *testing.T) {
	e := newEnv(t)
	admin, mod, plain, bob, carol := e.signup("admin"), e.signup("moddy"), e.signup("plain"), e.signup("bobby"), e.signup("carol")
	g := e.adminGroup("crew", admin, mod, plain, bob)
	e.befriend(mod, carol)
	base := "/api/chats/groups/" + g

	// only the admin hands out roles, and only known permissions
	e.expect(e.role(g, mod.Token, plain.ID, "moderator"), 403)
	e.expect(e.role(g, admin.Token, mod.ID, "admin"), 400)
	e.expect(e.role(g, admin.Token, mod.ID, "moderator", "everything"), 400)
	e.expect(e.role(g, admin.Token, admin.ID, "member"), 400)
	e.expect(e.role(g, admin.Token, carol.ID, "moderator"), 400) // not in the group
	modWS := e.dial(mod.Token)
	e.expect(e.role(g, admin.Token, mod.ID, "moderator", "add_members", "remove_members", "delete_messages"), 204)
	if ev := readEvent(t, modWS); ev.Event != "GroupUpdated" || !strings.Contains(string(ev.Payload), `"role"`) {
		t.Fatalf("mod got %s %s", ev.Event, ev.Payload)
	}
	e.expect(e.role(g, admin.Token, plain.ID, "moderator"), 204) // a moderator with no permissions

	var d struct {
		Members []struct {
			UserID      string   `json:"user_id"`
			Role        string   `json:"role"`
			Permissions []string `json:"permissions"`
		} `json:"members"`
	}
	e.expect(e.do("GET", base, bob.Token, nil), 200).json(t, &d)
	if d.Members[0].Role != "admin" || d.Members[1].Role != "moderator" || d.Members[1].UserID != mod.ID ||
		strings.Join(d.Members[1].Permissions, ",") != "add_members,remove_members,delete_messages" {
		t.Fatalf("members %+v", d.Members)
	}

	// what the moderator may do
	e.expect(e.do("POST", base+"/members", mod.Token, map[string]string{"user_id": carol.ID}), 201)
	e.expect(e.do("PATCH", base, mod.Token, map[string]string{"group_name": "mine"}), 403) // no edit_info
	e.expect(e.do("POST", base+"/invite", mod.Token, nil), 403)                            // no manage_invite
	e.expect(e.do("DELETE", base+"/members/"+admin.ID, mod.Token, nil), 403)
	e.expect(e.do("DELETE", base+"/members/"+plain.ID, mod.Token, nil), 403) // another moderator
	e.expect(e.do("DELETE", base+"/members/"+carol.ID, mod.Token, nil), 204)
	e.expect(e.do("DELETE", base+"/members/"+bob.ID, plain.Token, nil), 403) // moderator without remove_members
	e.expect(e.do("DELETE", base, mod.Token, nil), 403)                      // deleting is the admin's
	e.expect(e.do("PATCH", base, mod.Token, map[string]string{"visibility": "public"}), 403)
	e.expect(e.do("PUT", base+"/admin", mod.Token, map[string]string{"user_id": mod.ID}), 403)

	// deleting messages: members' yes, the admin's and moderators' no
	byBob := e.send(bob, g, "public", "spam")
	byAdmin := e.send(admin, g, "public", "rules")
	byPlain := e.send(plain, g, "public", "hi")
	e.expect(e.do("DELETE", "/api/chat/messages/"+byAdmin, mod.Token, nil), 403)
	e.expect(e.do("DELETE", "/api/chat/messages/"+byPlain, mod.Token, nil), 403)
	e.expect(e.do("DELETE", "/api/chat/messages/"+byBob, plain.Token, nil), 403)
	bobWS := e.dial(bob.Token)
	e.expect(e.do("DELETE", "/api/chat/messages/"+byBob, mod.Token, nil), 204)
	ev := readEvent(t, bobWS)
	if ev.Event != "MsgDeleted" || !strings.Contains(string(ev.Payload), `"from_id":"`+bob.ID+`"`) || !strings.Contains(string(ev.Payload), `"by_id":"`+mod.ID+`"`) {
		t.Fatalf("bob got %s %s", ev.Event, ev.Payload)
	}
	e.expect(e.do("DELETE", "/api/chat/messages/"+byPlain, admin.Token, nil), 204) // the admin may
	// editing someone else's message stays impossible for everyone
	e.expect(e.do("PATCH", "/api/chat/messages/"+byAdmin, mod.Token, map[string]string{"msg": "x"}), 403)

	// a permission taken away is gone at once
	e.expect(e.role(g, admin.Token, mod.ID, "member"), 204)
	e.expect(e.do("DELETE", "/api/chat/messages/"+e.send(bob, g, "public", "again"), mod.Token, nil), 403)
	e.expect(e.do("DELETE", base+"/members/"+bob.ID, mod.Token, nil), 403)
}

func TestAdminLeaves(t *testing.T) {
	e := newEnv(t)
	admin, mod, early, late := e.signup("admin"), e.signup("moddy"), e.signup("early"), e.signup("later")
	g := e.adminGroup("crew", admin, early, mod, late)
	e.expect(e.role(g, admin.Token, mod.ID, "moderator"), 204)
	leave := "/api/chats/groups/" + g + "/members"

	// naming someone outside the group fails; naming a member works
	e.expect(e.do("DELETE", leave+"?successor="+e.signup("stranger").ID, admin.Token, nil), 400)
	e.expect(e.do("DELETE", leave+"?successor=nope", admin.Token, nil), 400)
	e.expect(e.do("DELETE", leave+"?successor="+late.ID, admin.Token, nil), 204)
	e.now(`SELECT role FROM member_table WHERE group_id = $1 AND member_id = $2`, "admin", g, late.ID)

	// without a choice, the moderator comes before the longer-serving member
	e.expect(e.do("DELETE", leave, late.Token, nil), 204)
	e.now(`SELECT role FROM member_table WHERE group_id = $1 AND member_id = $2`, "admin", g, mod.ID)
	e.expect(e.do("DELETE", leave, mod.Token, nil), 204)
	e.now(`SELECT role FROM member_table WHERE group_id = $1 AND member_id = $2`, "admin", g, early.ID)
	// the last one out deletes it
	e.expect(e.do("DELETE", leave, early.Token, nil), 204)
	e.now(`SELECT count(*)::text FROM chat_groups WHERE id = $1`, "0", g)
}

func TestPublicGroups(t *testing.T) {
	e := newEnv(t)
	admin, visitor := e.signup("admin"), e.signup("visitor")
	var pub, priv struct {
		ChatID string `json:"chat_id"`
	}
	e.expect(e.do("POST", "/api/chats/groups", admin.Token, map[string]any{"group_name": "Go Learners", "description": "all welcome", "max_mems": 50, "visibility": "public"}), 201).json(t, &pub)
	e.expect(e.do("POST", "/api/chats/groups", admin.Token, map[string]any{"group_name": "Go family", "max_mems": 5}), 201).json(t, &priv)
	e.expect(e.do("POST", "/api/chats/groups", admin.Token, map[string]any{"group_name": "x", "max_mems": 5, "visibility": "secret"}), 400)
	r, _ := e.doRaw("PUT", "/api/chats/groups/"+pub.ChatID+"/avatar", admin.Token, photoWithEXIF(t), nil)
	e.expect(r, 200)

	// search finds only public groups
	type found struct {
		Groups []struct {
			ID        string  `json:"id"`
			IsMember  *bool   `json:"is_member"`
			AvatarURL *string `json:"avatar_url"`
		} `json:"groups"`
	}
	var f found
	e.expect(e.do("GET", "/api/chats/groups/search?q=GO", visitor.Token, nil), 200).json(t, &f)
	if len(f.Groups) != 1 || f.Groups[0].ID != pub.ChatID || *f.Groups[0].IsMember || f.Groups[0].AvatarURL == nil {
		t.Fatalf("search: %+v", f)
	}
	e.expect(e.do("GET", "/api/chats/groups/search?q=g", visitor.Token, nil), 400)
	e.expect(e.do("GET", "/api/chats/groups/search?q=%25%25", visitor.Token, nil), 200).json(t, &f) // "%%" is literal
	if len(f.Groups) != 0 {
		t.Fatalf("wildcards matched: %+v", f)
	}

	// a public group's details (not its members) and picture are open
	var d struct {
		IsMember bool  `json:"is_member"`
		Members  []any `json:"members"`
	}
	e.expect(e.do("GET", "/api/chats/groups/"+pub.ChatID, visitor.Token, nil), 200).json(t, &d)
	if d.IsMember || d.Members != nil {
		t.Fatalf("visitor sees %+v", d)
	}
	r, _ = e.doRaw("GET", "/api/chats/groups/"+pub.ChatID+"/avatar", visitor.Token, nil, nil)
	e.expect(r, 200)
	e.expect(e.do("GET", "/api/chats/groups/"+priv.ChatID, visitor.Token, nil), 404)
	r, _ = e.doRaw("GET", "/api/chats/groups/"+priv.ChatID+"/avatar", visitor.Token, nil, nil)
	e.expect(r, 404)
	e.expect(e.do("GET", "/api/chat/groups/"+pub.ChatID+"/messages", visitor.Token, nil), 403) // messages are for members

	// joining: public with a button, private only with a code
	e.expect(e.do("POST", "/api/chats/groups/"+priv.ChatID+"/join", visitor.Token, nil), 404)
	e.expect(e.do("POST", "/api/chats/groups/"+pub.ChatID+"/join", visitor.Token, nil), 201)
	e.expect(e.do("POST", "/api/chats/groups/"+pub.ChatID+"/join", visitor.Token, nil), 409)
	e.expect(e.do("GET", "/api/chats/groups/"+pub.ChatID, visitor.Token, nil), 200).json(t, &d)
	if !d.IsMember || len(d.Members) != 2 {
		t.Fatalf("member sees %+v", d)
	}

	// the admin can make it private again: it drops out of search
	e.expect(e.do("PATCH", "/api/chats/groups/"+pub.ChatID, admin.Token, map[string]string{"visibility": "hidden"}), 400)
	e.expect(e.do("PATCH", "/api/chats/groups/"+pub.ChatID, admin.Token, map[string]string{"visibility": "private"}), 200)
	e.expect(e.do("GET", "/api/chats/groups/search?q=go", visitor.Token, nil), 200).json(t, &f)
	if len(f.Groups) != 0 {
		t.Fatalf("private group still found: %+v", f)
	}
	e.expect(e.do("POST", "/api/chats/groups/"+pub.ChatID+"/join", e.signup("late").Token, nil), 404)
}

func TestHistoryStartsWhenYouJoin(t *testing.T) {
	e := newEnv(t)
	admin, newbie := e.signup("admin"), e.signup("newbie")
	g := e.adminGroup("crew", admin)
	old := e.send(admin, g, "public", "before you came")
	e.expect(e.join(newbie.Token, e.invite(admin.Token, g)), 201)
	fresh := e.send(admin, g, "public", "welcome")

	got := e.groupTexts(newbie, g, "")
	if len(got) != 1 || got[0] != "welcome" {
		t.Fatalf("newbie sees %v", got)
	}
	if got := e.groupTexts(newbie, g, "?before="+fresh); len(got) != 0 {
		t.Fatalf("older page shows %v", got)
	}
	if got := e.groupTexts(admin, g, ""); len(got) != 2 {
		t.Fatalf("admin sees %v", got)
	}
	// older messages can't be reached by id either
	e.expect(e.do("PUT", "/api/chat/messages/"+old+"/reaction", newbie.Token, map[string]string{"emoji": "👍"}), 404)
	e.expect(e.do("GET", "/api/chat/messages/"+old+"/reactions", newbie.Token, nil), 404)

	// the conversation list shows the newest message they can see
	e.send(admin, g, "public", "x") // newer than both
	var c struct {
		Conversations []struct {
			LastMessage *struct {
				Msg string `json:"msg"`
			} `json:"last_message"`
		} `json:"conversations"`
	}
	g2 := e.adminGroup("quiet", admin)
	e.send(admin, g2, "public", "secret history")
	e.expect(e.join(newbie.Token, e.invite(admin.Token, g2)), 201)
	e.expect(e.do("GET", "/api/chats", newbie.Token, nil), 200).json(t, &c)
	for _, conv := range c.Conversations {
		if conv.LastMessage != nil && conv.LastMessage.Msg == "secret history" {
			t.Fatal("conversation list leaks a message from before joining")
		}
	}
}

func (e *env) groupTexts(u user, g, query string) []string {
	e.t.Helper()
	var p page
	e.expect(e.do("GET", "/api/chat/groups/"+g+"/messages"+query, u.Token, nil), 200).json(e.t, &p)
	return p.texts()
}

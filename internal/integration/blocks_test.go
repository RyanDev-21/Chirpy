//go:build integration

package integration

import (
	"strings"
	"testing"
)

type searchResult struct {
	Users []struct {
		ID string `json:"id"`
	} `json:"UserList"`
}

func (e *env) searchIDs(u user, q string) []string {
	e.t.Helper()
	var r searchResult
	e.expect(e.do("GET", "/api/users/search?q="+q, u.Token, nil), 200).json(e.t, &r)
	ids := make([]string, len(r.Users))
	for i, x := range r.Users {
		ids[i] = x.ID
	}
	return ids
}

func TestBlockUser(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	e.send(bob, alice.ID, "private", "hey")
	bobWS := e.dial(bob.Token)

	e.expect(e.do("POST", "/api/users/blocks", alice.Token, map[string]string{"user_id": alice.ID}), 400)
	e.expect(e.do("POST", "/api/users/blocks", alice.Token, map[string]string{"user_id": "00000000-0000-0000-0000-000000000001"}), 404)
	e.expect(e.do("POST", "/api/users/blocks", alice.Token, map[string]string{"user_id": bob.ID}), 204)
	e.expect(e.do("POST", "/api/users/blocks", alice.Token, map[string]string{"user_id": bob.ID}), 204) // idempotent

	// bob sees the friendship end, like an unfriend
	if ev := readEvent(t, bobWS); ev.Event != "RemoveFri" {
		t.Fatalf("bob got %s", ev.Event)
	}
	e.now(`SELECT count(*)::text FROM user_relationships`, "0")

	// no messages either way, no new requests, no search
	dm := map[string]string{"msg": "hi", "to_id": alice.ID, "type": "private"}
	e.expect(e.do("POST", "/api/chats/message", bob.Token, dm), 403)
	r := e.expect(e.do("POST", "/api/friends/requests", bob.Token, map[string]string{"to_id": alice.ID}), 404)
	if !strings.Contains(string(r.body), "user does not exist") {
		t.Fatalf("blocked user learns about the block: %s", r.body)
	}
	e.expect(e.do("POST", "/api/friends/requests", alice.Token, map[string]string{"to_id": bob.ID}), 409)
	if ids := e.searchIDs(bob, "alic"); len(ids) != 0 {
		t.Fatalf("bob finds alice: %v", ids)
	}
	if ids := e.searchIDs(alice, "bobb"); len(ids) != 0 {
		t.Fatalf("alice finds bob: %v", ids)
	}

	var list struct {
		Blocked []struct {
			UserID string `json:"user_id"`
			Name   string `json:"name"`
		} `json:"blocked"`
	}
	e.expect(e.do("GET", "/api/users/blocks", alice.Token, nil), 200).json(t, &list)
	if len(list.Blocked) != 1 || list.Blocked[0].UserID != bob.ID || list.Blocked[0].Name != "bobby" {
		t.Fatalf("block list %+v", list)
	}
	// history is kept
	var page editPage
	e.expect(e.do("GET", "/api/chat/"+bob.ID+"/messages", alice.Token, nil), 200).json(t, &page)
	if len(page.MsgList) != 1 {
		t.Fatalf("history after block %+v", page)
	}

	// unblocking makes requests possible again (not the old friendship)
	e.expect(e.do("DELETE", "/api/users/blocks/"+bob.ID, alice.Token, nil), 204)
	if ids := e.searchIDs(bob, "alic"); len(ids) != 1 {
		t.Fatalf("after unblock bob finds %v", ids)
	}
	e.befriend(bob, alice)
}

func TestBlockCancelsPendingRequests(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.expect(e.do("POST", "/api/friends/requests", bob.Token, map[string]string{"to_id": alice.ID}), 200)
	e.expect(e.do("POST", "/api/users/blocks", alice.Token, map[string]string{"user_id": bob.ID}), 204)
	var reqs struct {
		Pending map[string]any `json:"pending_ids"`
		Request map[string]any `json:"request_ids"`
	}
	e.expect(e.do("GET", "/api/friends/requests", alice.Token, nil), 200).json(t, &reqs)
	if len(reqs.Pending)+len(reqs.Request) != 0 {
		t.Fatalf("requests after block %+v", reqs)
	}
}

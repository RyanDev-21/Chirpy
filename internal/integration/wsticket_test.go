//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWebSocketTicket(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	ticket := func(u user) string {
		t.Helper()
		var out struct {
			Ticket string `json:"ticket"`
		}
		e.expect(e.do("POST", "/api/chats/ws/ticket", u.Token, nil), 200).json(t, &out)
		return out.Ticket
	}
	dial := func(tk string) (*websocket.Conn, int) {
		url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/api/chats/ws?ticket=" + tk
		c, res, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			return nil, res.StatusCode
		}
		t.Cleanup(func() { _ = c.Close() })
		return c, 101
	}
	e.expect(e.do("POST", "/api/chats/ws/ticket", "", nil), 401)

	tk := ticket(alice)
	c, code := dial(tk)
	if code != 101 {
		t.Fatalf("ticket refused: %d", code)
	}
	// it really is alice's connection
	e.send(bob, alice.ID, "private", "hi from bob")
	if ev := readEvent(t, c); ev.Event != "msg" {
		t.Fatalf("got %s", ev.Event)
	}
	if _, code := dial(tk); code != 401 {
		t.Fatalf("ticket reused: %d", code)
	}
	if _, code := dial("forged"); code != 401 {
		t.Fatalf("forged ticket: %d", code)
	}
	// a ticket made before "sign out everywhere" no longer works
	old := ticket(bob)
	e.expect(e.do("POST", "/api/users/me/logout-all", bob.Token, nil), 204)
	if _, code := dial(old); code != 401 {
		t.Fatalf("ticket outlived logout-all: %d", code)
	}
}

func TestDuplicateGroupNames(t *testing.T) {
	e := newEnv(t)
	a, b := e.signup("alice"), e.signup("bobby")
	for _, u := range []user{a, a, b} {
		e.expect(e.do("POST", "/api/chats/groups", u.Token, map[string]any{"group_name": "family", "max_mems": 5}), 201)
	}
}

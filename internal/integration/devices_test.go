//go:build integration

package integration

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTwoDevicesStayInSync(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	var tablet user // alice signs in again on another device
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": alice.email, "password": password}), 200).json(t, &tablet)
	phoneWS, tabletWS := e.dial(alice.Token), e.dial(tablet.Token)
	time.Sleep(50 * time.Millisecond)

	// bob's message reaches both of alice's devices
	e.send(bob, alice.ID, "private", "hello alice")
	if ev := readEvent(t, phoneWS); ev.Event != "msg" {
		t.Fatalf("phone got %s", ev.Event)
	}
	if ev := readEvent(t, tabletWS); ev.Event != "msg" {
		t.Fatalf("tablet got %s", ev.Event)
	}

	// what alice sends from her phone shows up on her tablet, with the chat
	e.send(alice, bob.ID, "private", "sent from my phone")
	ev := readEvent(t, tabletWS)
	var m struct {
		FromID  string `json:"from_id"`
		ToID    string `json:"to_id"`
		Content string `json:"msg"`
	}
	_ = json.Unmarshal(ev.Payload, &m)
	if ev.Event != "msg" || m.FromID != alice.ID || m.ToID != bob.ID || m.Content != "sent from my phone" {
		t.Fatalf("tablet got %s %+v", ev.Event, m)
	}
	_ = readEvent(t, phoneWS) // the sending device gets it too (dedupe by id)

	// signing out everywhere closes both
	e.expect(e.do("POST", "/api/users/me/logout-all", alice.Token, nil), 204)
	for _, ws := range []interface{ ReadMessage() (int, []byte, error) }{phoneWS, tabletWS} {
		if _, _, err := ws.ReadMessage(); err == nil {
			t.Fatal("a device stayed connected after logout-all")
		}
	}
}

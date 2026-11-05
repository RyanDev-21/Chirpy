//go:build integration

package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func callSend(t *testing.T, c *websocket.Conn, event string, payload any) {
	t.Helper()
	if err := c.WriteJSON(map[string]any{"event": event, "payload": payload}); err != nil {
		t.Fatal(err)
	}
}

// nextCall reads the next Call* event, skipping anything else.
func nextCall(t *testing.T, c *websocket.Conn) (string, map[string]any) {
	t.Helper()
	for {
		ev := readEvent(t, c)
		if strings.HasPrefix(ev.Event, "Call") {
			var p map[string]any
			_ = json.Unmarshal(ev.Payload, &p)
			return ev.Event, p
		}
	}
}

func TestVoiceCall(t *testing.T) {
	e := newEnv(t)
	alice, bob, mallory := e.signup("alice"), e.signup("bobby"), e.signup("mallory")
	e.befriend(alice, bob)
	e.device(bob, "tok-bob")

	// ICE servers: STUN only without a Cloudflare key
	var ice struct {
		Servers []struct {
			URLs []string `json:"urls"`
		} `json:"ice_servers"`
		Relay bool `json:"relay"`
	}
	e.expect(e.do("GET", "/api/calls/ice", alice.Token, nil), 200).json(t, &ice)
	if ice.Relay || len(ice.Servers) == 0 || !strings.HasPrefix(ice.Servers[0].URLs[0], "stun:") {
		t.Fatalf("ice %+v", ice)
	}
	e.expect(e.do("GET", "/api/calls/ice", "", nil), 401)

	aliceWS := e.dial(alice.Token)
	malloryWS := e.dial(mallory.Token)
	callSend(t, malloryWS, "CallStart", map[string]string{"to_id": alice.ID})
	if ev, p := nextCall(t, malloryWS); ev != "CallError" || p["error"] != "not_friends" {
		t.Fatalf("stranger call: %s %v", ev, p)
	}

	// bob is offline: his phone gets a data-only ringing push
	callSend(t, aliceWS, "CallStart", map[string]string{"to_id": bob.ID})
	ev, p := nextCall(t, aliceWS)
	if ev != "CallRinging" {
		t.Fatalf("alice got %s %v", ev, p)
	}
	callID := p["call_id"].(string)
	m := e.waitPush("tok-bob", 1)[0]
	if !m.DataOnly || m.Data["type"] != "call" || m.Data["call_id"] != callID || m.Data["from_name"] != "alice" {
		t.Fatalf("ring push %+v", m)
	}

	// the phone wakes up, connects, and rings
	bobWS := e.dial(bob.Token)
	if ev, p := nextCall(t, bobWS); ev != "CallIncoming" || p["call_id"] != callID || p["from_id"] != alice.ID {
		t.Fatalf("bob got %s %v", ev, p)
	}
	callSend(t, malloryWS, "CallAccept", map[string]string{"call_id": callID})
	if ev, _ := nextCall(t, malloryWS); ev != "CallError" {
		t.Fatal("a stranger answered someone else's call")
	}
	callSend(t, bobWS, "CallAccept", map[string]string{"call_id": callID})
	if ev, _ := nextCall(t, aliceWS); ev != "CallAccepted" {
		t.Fatalf("alice got %s", ev)
	}

	// offer and answer pass through unchanged
	offer := map[string]string{"type": "offer", "sdp": "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\n"}
	callSend(t, aliceWS, "CallSignal", map[string]any{"call_id": callID, "data": offer})
	ev, p = nextCall(t, bobWS)
	if d, _ := p["data"].(map[string]any); ev != "CallSignal" || p["from_id"] != alice.ID || d["sdp"] != offer["sdp"] {
		t.Fatalf("bob got %s %v", ev, p)
	}
	callSend(t, bobWS, "CallSignal", map[string]any{"call_id": callID, "data": map[string]string{"type": "answer", "sdp": "v=0"}})
	if ev, _ := nextCall(t, aliceWS); ev != "CallSignal" {
		t.Fatalf("alice got %s", ev)
	}
	callSend(t, bobWS, "CallEnd", map[string]string{"call_id": callID})
	for _, c := range []*websocket.Conn{aliceWS, bobWS} {
		if ev, p := nextCall(t, c); ev != "CallEnded" || p["reason"] != "ended" {
			t.Fatalf("got %s %v", ev, p)
		}
	}

	// the call log, from both sides
	var log struct {
		Calls []struct {
			ID        string `json:"id"`
			Direction string `json:"direction"`
			State     string `json:"state"`
			Peer      struct {
				UserID string `json:"user_id"`
			} `json:"peer"`
		} `json:"calls"`
	}
	e.expect(e.do("GET", "/api/calls", alice.Token, nil), 200).json(t, &log)
	if len(log.Calls) != 1 || log.Calls[0].ID != callID || log.Calls[0].Direction != "outgoing" || log.Calls[0].State != "ended" || log.Calls[0].Peer.UserID != bob.ID {
		t.Fatalf("alice's log %+v", log)
	}
	e.expect(e.do("GET", "/api/calls", bob.Token, nil), 200).json(t, &log)
	if len(log.Calls) != 1 || log.Calls[0].Direction != "incoming" {
		t.Fatalf("bob's log %+v", log)
	}
	e.expect(e.do("GET", "/api/calls", mallory.Token, nil), 200).json(t, &log)
	if len(log.Calls) != 0 {
		t.Fatal("mallory sees others' calls")
	}
	e.expect(e.do("GET", "/api/calls?before="+callID, mallory.Token, nil), 400) // not her call

	// blocking during a call ends it
	callSend(t, aliceWS, "CallStart", map[string]string{"to_id": bob.ID})
	_, p = nextCall(t, aliceWS)
	second := p["call_id"].(string)
	nextCall(t, bobWS) // CallIncoming
	callSend(t, bobWS, "CallAccept", map[string]string{"call_id": second})
	nextCall(t, aliceWS) // CallAccepted
	e.expect(e.do("POST", "/api/users/blocks", bob.Token, map[string]string{"user_id": alice.ID}), 204)
	if ev, p := nextCall(t, aliceWS); ev != "CallEnded" || p["reason"] != "unfriended" {
		t.Fatalf("alice got %s %v", ev, p)
	}
	callSend(t, aliceWS, "CallStart", map[string]string{"to_id": bob.ID})
	if ev, p := nextCall(t, aliceWS); ev != "CallError" || p["error"] != "not_friends" {
		t.Fatalf("calling someone who blocked you: %s %v", ev, p)
	}
}

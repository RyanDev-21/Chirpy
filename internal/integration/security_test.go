//go:build integration

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/config"
	"github.com/gorilla/websocket"
)

func TestRefreshTokensAreStoredHashed(t *testing.T) {
	e := newEnv(t)
	u := e.signup("hashme")
	sum := sha256.Sum256([]byte(u.RefreshToken))
	e.now(`SELECT count(*)::text FROM refresh_tokens WHERE token = $1`, "0", u.RefreshToken)
	e.now(`SELECT count(*)::text FROM refresh_tokens WHERE token = $1`, "1", hex.EncodeToString(sum[:]))
	// the token still works for the client
	e.expect(e.do("POST", "/api/refresh", u.RefreshToken, nil), 200)
}

func TestWebsocketRejectsTokenInURL(t *testing.T) {
	e := newEnv(t)
	u := e.signup("wsurl")
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/api/chats/ws?token=" + u.Token
	_, res, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("websocket accepted a token in the URL")
	}
	if res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", res)
	}
}

func TestTypingAndSeenOnlyBetweenFriends(t *testing.T) {
	e := newEnv(t)
	alice, bob, mallory := e.signup("alice"), e.signup("bob"), e.signup("mallory")
	var r struct {
		ReqID string `json:"req_id"`
	}
	e.expect(e.do("POST", "/api/friends/requests", alice.Token, map[string]string{"to_id": bob.ID}), 200).json(t, &r)
	e.expect(e.do("PUT", "/api/friends/requests/"+r.ReqID, bob.Token, map[string]string{"status": "confirm"}), 201)

	bobWS := e.dial(bob.Token)
	malloryWS := e.dial(mallory.Token)
	aliceWS := e.dial(alice.Token)

	send := func(c *websocket.Conn, event, to string) {
		t.Helper()
		if err := c.WriteJSON(map[string]any{"event": event, "payload": map[string]string{"to_id": to, "msg_id": "00000000-0000-0000-0000-000000000000"}}); err != nil {
			t.Fatal(err)
		}
	}

	// the stranger goes first; if anything of theirs got through it would
	// reach bob before alice's event. (A timed-out read breaks a gorilla
	// connection, so "nothing arrives" is checked by ordering instead.)
	send(malloryWS, "Typing", bob.ID)
	send(malloryWS, "Seen", bob.ID)
	time.Sleep(300 * time.Millisecond)
	send(aliceWS, "Typing", bob.ID)

	ev := readEvent(t, bobWS)
	var p struct {
		FromID string `json:"from_id"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if ev.Event != "Typing" || p.FromID != alice.ID {
		t.Fatalf("bob's first event = %s from %s; want Typing from alice (stranger events must be dropped)", ev.Event, p.FromID)
	}

	// REST read receipts must point at a message in that chat
	e.expect(e.do("POST", "/api/chat/"+bob.ID+"/messages", mallory.Token, map[string]string{"msg_id": "00000000-0000-0000-0000-000000000001"}), 400)
}

// Behind Render (one proxy) each client gets its own budget, and entries a
// client adds to X-Forwarded-For itself cannot move it to a fresh budget.
func TestRateLimitUsesRealClientBehindProxy(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.TrustedProxies = 1 })
	login := func(xff string) int {
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/login", strings.NewReader(`{"email":"x@example.io","password":"nope"}`))
		req.Header.Set("X-Forwarded-For", xff)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	// a different spoofed prefix each time; the proxy-added hop stays the same
	e.assertLimited(10, func(i int) int { return login(fmt.Sprintf("10.9.9.%d, 203.0.113.7", i)) })
	if c := login("198.51.100.20"); c != 401 {
		t.Fatalf("another client was blocked by the first one's budget: %d", c)
	}
}

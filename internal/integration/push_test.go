//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/push"
)

// fakePush records notifications instead of calling FCM.
type fakePush struct {
	mu   sync.Mutex
	sent []push.Message
	dead map[string]bool
}

func (f *fakePush) Send(_ context.Context, m push.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead[m.Token] {
		return push.ErrDeadToken
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakePush) to(token string) []push.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []push.Message
	for _, m := range f.sent {
		if m.Token == token {
			out = append(out, m)
		}
	}
	return out
}

// waitPush waits until token has n notifications; settle waits a moment
// and checks nothing more arrives (jobs run in the background).
func (e *env) waitPush(token string, n int) []push.Message {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := e.push.to(token); len(got) >= n {
			time.Sleep(200 * time.Millisecond)
			if got = e.push.to(token); len(got) != n {
				e.t.Fatalf("token %s: %d notifications, want %d: %+v", token, len(got), n, got)
			}
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := e.push.to(token)
	if len(got) != n {
		e.t.Fatalf("token %s: %d notifications, want %d: %+v", token, len(got), n, got)
	}
	return got
}

func (e *env) device(u user, token string) {
	e.t.Helper()
	e.expect(e.do("POST", "/api/devices", u.Token, map[string]string{"token": token}), 204)
}

func TestPushForMessages(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	e.device(bob, "tok-bob")

	e.send(alice, bob.ID, "private", "are you free?")
	m := e.waitPush("tok-bob", 1)[0]
	if m.Title != "alice" || m.Body != "are you free?" || m.Data["type"] != "private" || m.Data["from_id"] != alice.ID || m.Tag != "dm:"+alice.ID {
		t.Fatalf("dm notification %+v", m)
	}

	// online: the live event is enough
	ws := e.dial(bob.Token)
	e.send(alice, bob.ID, "private", "still there?")
	_ = readEvent(t, ws)
	e.waitPush("tok-bob", 1)
	_ = ws.Close()
	time.Sleep(100 * time.Millisecond)

	// switched off, then muted
	e.expect(e.do("PUT", "/api/users/me/notifications", bob.Token, map[string]bool{"private": false, "groups": true, "friends": true, "reactions": true}), 200)
	e.send(alice, bob.ID, "private", "hello?")
	e.waitPush("tok-bob", 1)
	e.expect(e.do("PUT", "/api/users/me/notifications", bob.Token, map[string]bool{"private": true, "groups": true, "friends": true, "reactions": true}), 200)
	e.expect(e.do("PUT", "/api/mutes", bob.Token, map[string]any{"type": "private", "id": alice.ID, "until": time.Now().Add(-time.Hour)}), 400)
	e.expect(e.do("PUT", "/api/mutes", bob.Token, map[string]any{"type": "private", "id": alice.ID}), 204)
	e.send(alice, bob.ID, "private", "muted?")
	e.waitPush("tok-bob", 1)
	var mutes struct {
		Mutes []struct {
			ID string `json:"id"`
		} `json:"mutes"`
	}
	e.expect(e.do("GET", "/api/mutes", bob.Token, nil), 200).json(t, &mutes)
	if len(mutes.Mutes) != 1 || mutes.Mutes[0].ID != alice.ID {
		t.Fatalf("mutes %+v", mutes)
	}
	e.expect(e.do("DELETE", "/api/mutes", bob.Token, map[string]any{"type": "private", "id": alice.ID}), 204)
	e.send(alice, bob.ID, "private", "back")
	e.waitPush("tok-bob", 2)

	// reactions go to the author only
	e.device(alice, "tok-alice")
	e.expect(e.do("PUT", "/api/chat/messages/"+e.send(alice, bob.ID, "private", "react")+"/reaction", bob.Token, map[string]string{"emoji": "❤️"}), 204)
	if r := e.waitPush("tok-alice", 1)[0]; r.Body != "reacted ❤️ to your message" || r.Title != "bobby" {
		t.Fatalf("reaction notification %+v", r)
	}
}

func TestPushForGroupsAndFriends(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.signup("alice"), e.signup("bobby"), e.seedUser("carol")
	e.device(bob, "tok-bob")
	e.device(carol, "tok-carol")
	g := e.adminGroup("devs", alice, bob)
	e.send(alice, g, "public", "standup at 10")
	if m := e.waitPush("tok-bob", 1)[0]; m.Title != "devs" || m.Body != "alice: standup at 10" || m.Data["group_id"] != g {
		t.Fatalf("group notification %+v", m)
	}

	var r struct {
		ReqID string `json:"req_id"`
	}
	e.expect(e.do("POST", "/api/friends/requests", bob.Token, map[string]string{"to_id": carol.ID}), 200).json(t, &r)
	if m := e.waitPush("tok-carol", 1)[0]; m.Body != "bobby sent you a friend request" {
		t.Fatalf("request notification %+v", m)
	}
	e.expect(e.do("PUT", "/api/friends/requests/"+r.ReqID, carol.Token, map[string]string{"status": "confirm"}), 201)
	if m := e.waitPush("tok-bob", 2)[1]; m.Body != "carol accepted your friend request" {
		t.Fatalf("accept notification %+v", m)
	}
}

func TestDeviceLifecycle(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	e.expect(e.do("POST", "/api/devices", bob.Token, map[string]string{"token": ""}), 400)
	e.expect(e.do("POST", "/api/devices", bob.Token, map[string]string{"token": "t", "platform": "windows-phone"}), 400)

	// a token FCM reports dead is forgotten
	e.device(bob, "tok-dead")
	e.push.dead["tok-dead"] = true
	e.send(alice, bob.ID, "private", "x")
	e.eventually(`SELECT count(*)::text FROM user_devices WHERE token = 'tok-dead'`, "0")

	// the same phone signing in as someone else moves to that account
	e.device(bob, "tok-phone")
	e.device(alice, "tok-phone")
	e.now(`SELECT user_id::text FROM user_devices WHERE token = 'tok-phone'`, alice.ID)

	// logout removes it; a password change removes all of the user's phones
	e.expect(e.do("DELETE", "/api/devices", alice.Token, map[string]string{"token": "tok-phone"}), 204)
	e.now(`SELECT count(*)::text FROM user_devices WHERE token = 'tok-phone'`, "0")
	e.device(bob, "tok-bob")
	e.expect(e.do("POST", "/api/users/password", bob.Token, map[string]string{"old_password": password, "new_password": password + "-2"}), 200)
	e.now(`SELECT count(*)::text FROM user_devices WHERE user_id = $1`, "0", bob.ID)
}

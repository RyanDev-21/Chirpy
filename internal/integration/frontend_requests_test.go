//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// The other person's Seen marker comes back with private history, for
// friends only.
func TestPeerSeenID(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	e.send(alice, bob.ID, "private", "one")
	last := e.send(alice, bob.ID, "private", "two")

	type hist struct {
		PeerSeenID *string `json:"peer_seen_id"`
	}
	peerSeen := func() *string {
		t.Helper()
		var h hist
		e.expect(e.do("GET", "/api/chat/"+bob.ID+"/messages", alice.Token, nil), 200).json(t, &h)
		return h.PeerSeenID
	}
	if got := peerSeen(); got != nil {
		t.Fatalf("nothing seen yet, got %s", *got)
	}
	e.expect(e.do("POST", "/api/chat/"+alice.ID+"/messages", bob.Token, map[string]string{"msg_id": last}), 201)
	deadline := time.Now().Add(10 * time.Second) // markers are written by a background job
	for {
		if got := peerSeen(); got != nil {
			if *got != last {
				t.Fatalf("peer_seen_id %s, want %s", *got, last)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("peer_seen_id never appeared")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// bob's own marker isn't echoed back to him as the peer's
	var h hist
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &h)
	if h.PeerSeenID != nil {
		t.Fatalf("alice saw nothing, bob got %s", *h.PeerSeenID)
	}

	// no longer friends: the marker is hidden
	e.expect(e.do("POST", "/api/users/blocks", bob.Token, map[string]string{"user_id": alice.ID}), 204)
	if got := peerSeen(); got != nil {
		t.Fatalf("after the block alice still sees %s", *got)
	}
}

// Group history takes since: only newer messages, never from before joining.
func TestGroupHistorySince(t *testing.T) {
	e := newEnv(t)
	leader, ann, late := e.signup("leader"), e.signup("annie"), e.signup("later")
	g := e.adminGroup("crew", leader, ann)
	before := e.send(leader, g, "public", "g1")
	e.send(leader, g, "public", "g2")
	e.expect(e.join(late.Token, e.invite(leader.Token, g)), 201)
	e.send(ann, g, "public", "g3")

	var at time.Time
	if err := e.db.QueryRow(`SELECT created_at FROM GroupMessage WHERE id = $1`, before).Scan(&at); err != nil {
		t.Fatal(err)
	}
	since := at.UTC().Format(time.RFC3339Nano)
	base := "/api/chat/groups/" + g + "/messages?since=" + since

	var p page
	e.expect(e.do("GET", base, ann.Token, nil), 200).json(t, &p)
	if got := strings.Join(p.texts(), ","); got != "g2,g3" {
		t.Fatalf("ann since g1: %s", got)
	}
	p = page{}
	e.expect(e.do("GET", base, late.Token, nil), 200).json(t, &p)
	if got := strings.Join(p.texts(), ","); got != "g3" {
		t.Fatalf("late joiner since g1: %s", got)
	}
	e.expect(e.do("GET", "/api/chat/groups/"+g+"/messages?since=yesterday", ann.Token, nil), 400)
	e.expect(e.do("GET", "/api/chat/groups/"+g+"/messages?since="+since, e.signup("outsider").Token, nil), 403)
}

// The chat list shows who sent a group's last message; the blocked list
// has display names and pictures.
func TestListsCarryNames(t *testing.T) {
	e := newEnv(t)
	leader, ann := e.signup("leader"), e.signup("annie")
	g := e.adminGroup("crew", leader, ann)
	e.send(ann, g, "public", "found a trail")

	var c struct {
		Conversations []struct {
			ID   string `json:"id"`
			Last *struct {
				FromName string `json:"from_name"`
			} `json:"last_message"`
		} `json:"conversations"`
	}
	e.expect(e.do("GET", "/api/chats", leader.Token, nil), 200).json(t, &c)
	found := false
	for _, conv := range c.Conversations {
		if conv.ID == g {
			found = true
			if conv.Last == nil || conv.Last.FromName != "annie" {
				t.Fatalf("group last message %+v", conv.Last)
			}
		}
	}
	if !found {
		t.Fatal("group missing from the chat list")
	}

	e.expect(e.do("PATCH", "/api/users/me", ann.Token, map[string]string{"display_name": "Annie B"}), 200)
	e.expect(e.do("POST", "/api/users/blocks", leader.Token, map[string]string{"user_id": ann.ID}), 204)
	var list struct {
		Blocked []struct {
			UserID      string  `json:"user_id"`
			DisplayName string  `json:"display_name"`
			AvatarURL   *string `json:"avatar_url"`
		} `json:"blocked"`
	}
	r := e.expect(e.do("GET", "/api/users/blocks", leader.Token, nil), 200)
	r.json(t, &list)
	if len(list.Blocked) != 1 || list.Blocked[0].DisplayName != "Annie B" || !strings.Contains(string(r.body), `"avatar_url"`) {
		t.Fatalf("blocked list %s", r.body)
	}
}

// Apps send an Origin equal to the API's own address; that is accepted,
// a foreign one is not.
func TestWSSameHostOrigin(t *testing.T) {
	e := newEnv(t)
	u := e.signup("alice")
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/api/chats/ws"
	dial := func(origin string) int {
		t.Helper()
		conn, res, err := websocket.DefaultDialer.Dial(url, http.Header{"Authorization": {"Bearer " + u.Token}, "Origin": {origin}})
		if err == nil {
			_ = conn.Close()
			return 101
		}
		if res == nil {
			t.Fatal(err)
		}
		return res.StatusCode
	}
	if got := dial(e.srv.URL); got != 101 {
		t.Fatalf("same-host origin got %d", got)
	}
	if got := dial("https://evil.example"); got != 403 {
		t.Fatalf("foreign origin got %d", got)
	}
}

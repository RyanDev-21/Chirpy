//go:build integration

package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

type forwardResults struct {
	Results []struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		MsgID string `json:"msg_id"`
		Error string `json:"error"`
	} `json:"results"`
}

func TestForward(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol, dave, late := e.signup("alice"), e.signup("bobby"), e.signup("carol"), e.signup("davey"), e.signup("later")
	e.befriend(alice, bob)
	e.befriend(alice, carol)
	g := e.adminGroup("crew", alice, carol)

	photo := e.uploadFile(bob, "image", "image/jpeg", []byte{0xFF, 0xD8, 0xFF, 7, 7, 7})
	var sent struct {
		MsgID string `json:"msg_id"`
	}
	e.expect(e.do("POST", "/api/chats/message", bob.Token, map[string]any{"msg": "look at this", "to_id": alice.ID, "type": "private", "media": []string{photo}}), 200).json(t, &sent)

	forward := func(u user, msg string, to []map[string]string, code int) forwardResults {
		t.Helper()
		var r forwardResults
		res := e.expect(e.do("POST", "/api/chats/forward", u.Token, map[string]any{"message_id": msg, "to": to}), code)
		if code == 200 {
			res.json(t, &r)
		}
		return r
	}

	carolWS := e.dial(carol.Token)
	r := forward(alice, sent.MsgID, []map[string]string{
		{"type": "private", "id": carol.ID},
		{"type": "private", "id": dave.ID}, // not a friend
		{"type": "public", "id": g},
	}, 200)
	if len(r.Results) != 3 || r.Results[0].MsgID == "" || r.Results[1].Error == "" || r.Results[2].MsgID == "" {
		t.Fatalf("results %+v", r)
	}

	// carol gets it live and in history, labelled forwarded, with her own copy of the photo
	var ev wsEvent
	for ev = readEvent(t, carolWS); ev.Event != "msg"; ev = readEvent(t, carolWS) {
	}
	var live struct {
		FromID    string `json:"from_id"`
		Content   string `json:"msg"`
		Forwarded bool   `json:"forwarded"`
		Media     []struct {
			ID string `json:"id"`
		} `json:"media"`
	}
	_ = json.Unmarshal(ev.Payload, &live)
	if !live.Forwarded || live.FromID != alice.ID || live.Content != "look at this" || len(live.Media) != 1 || live.Media[0].ID == photo {
		t.Fatalf("carol got %s", ev.Payload)
	}
	if strings.Contains(string(ev.Payload), bob.ID) {
		t.Fatal("the original sender is revealed")
	}
	if !e.r2.has(live.Media[0].ID) {
		t.Fatal("the photo wasn't copied in storage")
	}
	e.expect(e.do("GET", "/api/media/"+live.Media[0].ID, carol.Token, nil), 200)
	var page struct {
		MsgList []struct {
			Forwarded bool `json:"forwarded"`
		} `json:"msgList"`
	}
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", carol.Token, nil), 200).json(t, &page)
	if len(page.MsgList) != 1 || !page.MsgList[0].Forwarded {
		t.Fatalf("history %+v", page)
	}

	// only what you can see; at most 5 targets
	forward(dave, sent.MsgID, []map[string]string{{"type": "private", "id": alice.ID}}, 404)
	six := make([]map[string]string, 6)
	for i := range six {
		six[i] = map[string]string{"type": "private", "id": carol.ID}
	}
	forward(alice, sent.MsgID, six, 400)
	forward(alice, sent.MsgID, nil, 400)

	// a group message from before someone joined can't be forwarded by them
	old := e.send(alice, g, "public", "before you came")
	e.expect(e.join(late.Token, e.invite(alice.Token, g)), 201)
	e.befriend(late, alice)
	forward(late, old, []map[string]string{{"type": "private", "id": alice.ID}}, 404)

	// expired media: the target reports it, the app re-uploads instead
	if _, err := e.db.Exec(`UPDATE media SET deleted_at = NOW() WHERE id = $1`, photo); err != nil {
		t.Fatal(err)
	}
	r = forward(alice, sent.MsgID, []map[string]string{{"type": "private", "id": carol.ID}}, 200)
	if r.Results[0].MsgID != "" || !strings.Contains(r.Results[0].Error, "expired") {
		t.Fatalf("expired media: %+v", r)
	}

	// clients can't label their own messages forwarded
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"msg": "mine", "to_id": bob.ID, "type": "private", "forwarded": true}), 200)
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &page)
	if last := page.MsgList[len(page.MsgList)-1]; last.Forwarded {
		t.Fatal("a client set forwarded itself")
	}
}

func TestForwardCountsAgainstTheFileQuota(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.signup("alice"), e.signup("bobby"), e.signup("carol")
	e.befriend(alice, bob)
	e.befriend(alice, carol)
	photo := e.uploadFile(bob, "image", "image/jpeg", []byte{0xFF, 0xD8, 0xFF, 1})
	var sent struct {
		MsgID string `json:"msg_id"`
	}
	e.expect(e.do("POST", "/api/chats/message", bob.Token, map[string]any{"msg": "", "to_id": alice.ID, "type": "private", "media": []string{photo}}), 200).json(t, &sent)

	// alice already made 300 tiny files today: no more copies, no more uploads
	if _, err := e.db.Exec(`INSERT INTO media (id, owner_id, kind, content_type, size, created_at, expires_at, last_access_at)
		SELECT gen_random_uuid(), $1, 'image', 'image/jpeg', 10, NOW(), NOW() + interval '1 day', NOW() FROM generate_series(1, 300)`, alice.ID); err != nil {
		t.Fatal(err)
	}
	var r forwardResults
	e.expect(e.do("POST", "/api/chats/forward", alice.Token, map[string]any{"message_id": sent.MsgID, "to": []map[string]string{{"type": "private", "id": carol.ID}}}), 200).json(t, &r)
	if r.Results[0].MsgID != "" || !strings.Contains(r.Results[0].Error, "300 files") {
		t.Fatalf("forward over the file quota: %+v", r)
	}
	e.expect(e.do("POST", "/api/media", alice.Token, map[string]any{"kind": "image", "content_type": "image/png", "size": 10}), 429)
}

//go:build integration

package integration

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"RyanDev-21.com/Chirpy/internal/push"
)

// uploadBigVideo uploads a 20 MB video (over the 16 MB "big" line).
func (e *env) uploadBigVideo(u user) string {
	e.t.Helper()
	body := bytes.Repeat([]byte{1}, 20<<20)
	var up upload
	e.expect(e.do("POST", "/api/media", u.Token, map[string]any{"kind": "video", "content_type": "video/mp4", "size": len(body), "duration_ms": 60_000}), 201).json(e.t, &up)
	req, _ := http.NewRequest("PUT", up.URL, bytes.NewReader(body))
	for k, v := range up.Headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		e.t.Fatalf("upload: %v %v", err, res)
	}
	res.Body.Close()
	return up.ID
}

func TestBigFilePushes(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.signup("alice"), e.signup("bobby"), e.signup("carol")
	e.befriend(alice, bob)
	g := e.adminGroup("crew", alice, bob, carol)
	e.device(bob, "tok-bob")
	e.device(carol, "tok-carol")

	priv, grp := e.uploadBigVideo(alice), e.uploadBigVideo(alice)
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"to_id": bob.ID, "type": "private", "media": []string{priv}}), 200)
	// sending it is an ordinary message push: no warning yet
	m := e.waitPush("tok-bob", 1)[0]
	if m.Channel != "" || m.Body != "🎥 Video" || m.Title != "alice" {
		t.Fatalf("private push %+v", m)
	}
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"to_id": g, "type": "public", "media": []string{grp}}), 200)
	if m := e.waitPush("tok-bob", 2)[1]; m.Channel != "" || m.Body != "alice: 🎥 Video" {
		t.Fatalf("group push %+v", m)
	}
	e.waitPush("tok-carol", 1)

	// carol downloads hers; bob downloads nothing
	e.expect(e.do("GET", "/api/media/"+grp, carol.Token, nil), 200)

	// not yet the last day: no reminder
	if err := e.app.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.waitPush("tok-bob", 2)
	// on the last day (20 hours left), the hourly cleanup reminds bob (twice:
	// one per file) and not carol, who has it already
	if _, err := e.db.Exec(`UPDATE media SET expires_at = now() AT TIME ZONE 'UTC' + interval '20 hours' WHERE id = ANY($1)`, []string{priv, grp}); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := e.waitPush("tok-bob", 4)[2:]
	titles := map[string]push.Message{}
	for _, r := range got {
		if r.Channel != push.ChannelBigFiles || r.Data["type"] != push.KindBigFile || !strings.HasSuffix(r.Body, "will be deleted in 20 hours") {
			t.Fatalf("reminder %+v", r)
		}
		titles[r.Title] = r
	}
	if p, ok := titles["alice"]; !ok || p.Body != "Video from alice will be deleted in 20 hours" || p.Data["media_id"] != priv {
		t.Fatalf("private reminder %+v", titles)
	}
	if p, ok := titles["crew"]; !ok || p.Data["group_id"] != g || p.Data["media_id"] != grp {
		t.Fatalf("group reminder %+v", titles)
	}
	// once per file
	if err := e.app.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.waitPush("tok-bob", 4)
	e.waitPush("tok-carol", 1)
}

// Muting the person or the group silences their reminders too.
func TestBigFileReminderRespectsMutes(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	g := e.adminGroup("crew", alice, bob)
	e.device(bob, "tok-bob")
	e.expect(e.do("PUT", "/api/mutes", bob.Token, map[string]string{"type": "private", "id": alice.ID}), 204)
	e.expect(e.do("PUT", "/api/mutes", bob.Token, map[string]string{"type": "public", "id": g}), 204)

	priv, grp := e.uploadBigVideo(alice), e.uploadBigVideo(alice)
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"to_id": bob.ID, "type": "private", "media": []string{priv}}), 200)
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"to_id": g, "type": "public", "media": []string{grp}}), 200)
	if _, err := e.db.Exec(`UPDATE media SET expires_at = now() AT TIME ZONE 'UTC' + interval '20 hours' WHERE id = ANY($1)`, []string{priv, grp}); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.waitPush("tok-bob", 0) // nothing: both chats are muted
	e.now(`SELECT count(*)::text FROM media WHERE reminded_at IS NOT NULL AND id = ANY($1)`, "2", []string{priv, grp})
}

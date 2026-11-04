//go:build integration

package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/media"
)

// fakeR2 is an in-memory object store behind real upload/download links.
// Like R2 with a signed PUT, it refuses a body whose size or type differs
// from what the link was issued for.
type fakeR2 struct {
	mu      sync.Mutex
	objects map[string][]byte
	srv     *httptest.Server
}

func newFakeR2(t *testing.T) *fakeR2 {
	f := &fakeR2{objects: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/")
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			if _, exists := f.objects[key]; exists && r.Header.Get("If-None-Match") == "*" {
				http.Error(w, "PreconditionFailed", http.StatusPreconditionFailed)
				return
			}
			if r.Header.Get("If-None-Match") != "*" {
				http.Error(w, "SignatureDoesNotMatch: if-none-match is signed", http.StatusForbidden)
				return
			}
			if strconv.Itoa(len(body)) != r.URL.Query().Get("size") || r.Header.Get("Content-Type") != r.URL.Query().Get("ct") {
				http.Error(w, "SignatureDoesNotMatch", http.StatusForbidden)
				return
			}
			f.objects[key] = body
		case http.MethodGet:
			b, ok := f.objects[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeR2) PresignPut(key, ct string, size int64, _ time.Duration) (string, error) {
	return f.srv.URL + "/" + key + "?ct=" + url.QueryEscape(ct) + "&size=" + strconv.FormatInt(size, 10), nil
}
func (f *fakeR2) PresignGet(key, _ string, _ time.Duration) (string, error) {
	return f.srv.URL + "/" + key, nil
}
func (f *fakeR2) Size(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	if !ok {
		return 0, media.ErrNoObject
	}
	return int64(len(b)), nil
}
func (f *fakeR2) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return nil
}
func (f *fakeR2) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects["m/"+id]
	return ok
}

type upload struct {
	ID      string            `json:"media_id"`
	URL     string            `json:"upload_url"`
	Headers map[string]string `json:"headers"`
}

// uploadFile asks for a link and PUTs body to it.
func (e *env) uploadFile(u user, kind, ct string, body []byte) string {
	e.t.Helper()
	var up upload
	e.expect(e.do("POST", "/api/media", u.Token, map[string]any{"kind": kind, "content_type": ct, "size": len(body)}), 201).json(e.t, &up)
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

type mediaPage struct {
	MsgList []struct {
		ID    string `json:"message_id"`
		Media []struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Expired bool   `json:"expired"`
		} `json:"media"`
	} `json:"msgList"`
}

func TestPhotoMessageEndToEnd(t *testing.T) {
	e := newEnv(t)
	alice, bob, mallory := e.signup("alice"), e.signup("bobby"), e.signup("mallory")
	e.befriend(alice, bob)
	e.device(bob, "tok-bob")
	photo := bytes.Repeat([]byte{0xFF, 0xD8, 0xFF}, 300)
	id := e.uploadFile(alice, "image", "image/jpeg", photo)

	// photo-only message
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"to_id": bob.ID, "type": "private", "media": []string{id}}), 200)
	if m := e.waitPush("tok-bob", 1)[0]; m.Body != "📷 Photo" {
		t.Fatalf("push body %q", m.Body)
	}
	var page mediaPage
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &page)
	if len(page.MsgList) != 1 || len(page.MsgList[0].Media) != 1 || page.MsgList[0].Media[0].ID != id {
		t.Fatalf("history %+v", page)
	}

	// only the two of them can download it
	e.expect(e.do("GET", "/api/media/"+id, mallory.Token, nil), 404)
	var dl struct {
		URL string `json:"url"`
	}
	e.expect(e.do("GET", "/api/media/"+id, bob.Token, nil), 200).json(t, &dl)
	res, err := http.Get(dl.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Equal(got, photo) {
		t.Fatal("downloaded bytes differ")
	}
	e.now(`SELECT (delivered_at IS NOT NULL)::text FROM media WHERE id = $1`, "true", id)

	// 24 h after delivery it is removed; history then marks it expired
	if _, err := e.db.Exec(`UPDATE media SET delivered_at = delivered_at - interval '25 hours' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.r2.has(id) {
		t.Fatal("delivered media not deleted from storage")
	}
	e.expect(e.do("GET", "/api/media/"+id, bob.Token, nil), 410)
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &page)
	if !page.MsgList[0].Media[0].Expired {
		t.Fatalf("expired flag missing: %+v", page.MsgList[0].Media)
	}
}

func TestMediaValidationAndLimits(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	post := func(kind, ct string, size int) int {
		return e.do("POST", "/api/media", alice.Token, map[string]any{"kind": kind, "content_type": ct, "size": size}).code
	}
	for _, c := range []struct {
		kind, ct string
		size     int
	}{{"image", "image/gif", 10}, {"image", "image/jpeg", 6 << 20}, {"video", "video/mp4", 26 << 20}, {"audio", "audio/mpeg", 10}, {"image", "image/jpeg", 0}} {
		if got := post(c.kind, c.ct, c.size); got != 400 {
			t.Errorf("%+v: %d, want 400", c, got)
		}
	}
	// the upload link only takes the declared file
	var up upload
	e.expect(e.do("POST", "/api/media", alice.Token, map[string]any{"kind": "image", "content_type": "image/png", "size": 10}), 201).json(t, &up)
	req, _ := http.NewRequest("PUT", up.URL, bytes.NewReader(make([]byte, 5000)))
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("If-None-Match", "*")
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != 403 {
		t.Fatalf("oversized body accepted: %d", res.StatusCode)
	}
	send := func(ids ...string) int {
		return e.do("POST", "/api/chats/message", alice.Token, map[string]any{"msg": "x", "to_id": bob.ID, "type": "private", "media": ids}).code
	}
	if got := send(up.ID); got != 400 { // never uploaded
		t.Fatalf("attach without upload: %d", got)
	}
	bobs := e.uploadFile(bob, "image", "image/png", []byte("png!"))
	if got := send(bobs); got != 400 {
		t.Fatalf("attach someone else's: %d", got)
	}
	mine := e.uploadFile(alice, "video", "video/mp4", []byte("mp4 bytes"))
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"msg": "", "to_id": bob.ID, "type": "private", "media": []string{mine}}), 200)
	if got := send(mine); got != 400 {
		t.Fatalf("attach twice: %d", got)
	}
	many := make([]string, 11)
	for i := range many {
		many[i] = "00000000-0000-0000-0000-000000000000"
	}
	if got := send(many...); got != 400 {
		t.Fatalf("11 attachments: %d", got)
	}

	// daily quota and global cap
	if _, err := e.db.Exec(`INSERT INTO media (id, owner_id, kind, content_type, size, created_at, expires_at, last_access_at)
		VALUES (gen_random_uuid(), $1, 'video', 'video/mp4', 199000000, NOW(), NOW() + interval '7 days', NOW())`, alice.ID); err != nil {
		t.Fatal(err)
	}
	if got := post("video", "video/mp4", 20<<20); got != 429 {
		t.Fatalf("over daily quota: %d", got)
	}
	if _, err := e.db.Exec(`INSERT INTO media (id, owner_id, kind, content_type, size, created_at, expires_at, last_access_at)
		VALUES (gen_random_uuid(), $1, 'video', 'video/mp4', 9900000000, NOW() - interval '2 days', NOW() + interval '7 days', NOW())`, bob.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.do("POST", "/api/media", bob.Token, map[string]any{"kind": "image", "content_type": "image/jpeg", "size": 100}).code; got != 507 {
		t.Fatalf("storage full: %d", got)
	}
}

func TestMediaCleanupRules(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	old := e.uploadFile(alice, "image", "image/jpeg", []byte("old"))
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"to_id": bob.ID, "type": "private", "media": []string{old}}), 200)
	abandoned := e.uploadFile(alice, "image", "image/jpeg", []byte("never sent"))
	deletedMsgMedia := e.uploadFile(alice, "image", "image/jpeg", []byte("regret"))
	var sent struct {
		MsgID string `json:"msg_id"`
	}
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"to_id": bob.ID, "type": "private", "media": []string{deletedMsgMedia}}), 200).json(t, &sent)
	keep := e.uploadFile(alice, "image", "image/jpeg", []byte("fresh"))
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"to_id": bob.ID, "type": "private", "media": []string{keep}}), 200)

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE media SET expires_at = NOW() - interval '1 minute' WHERE id = $1`, old)           // 7 days passed, never downloaded
	exec(`UPDATE media SET created_at = created_at - interval '2 hours' WHERE id = $1`, abandoned) // upload never attached
	e.expect(e.do("DELETE", "/api/chat/messages/"+sent.MsgID, alice.Token, nil), 204)              // message deleted for everyone

	if err := e.app.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"7-day limit": old, "abandoned upload": abandoned, "deleted message": deletedMsgMedia} {
		if e.r2.has(id) {
			t.Errorf("%s: still stored", name)
		}
	}
	if !e.r2.has(keep) {
		t.Fatal("fresh media was deleted")
	}

	// LRU: above 9 GB, least recently downloaded files go first, down to 8 GB
	exec(`INSERT INTO media (id, owner_id, kind, content_type, size, status, created_at, expires_at, last_access_at)
		SELECT gen_random_uuid(), $1, 'video', 'video/mp4', 1073741824, 'ready', NOW(), NOW() + interval '7 days', NOW() - (g || ' hours')::interval
		FROM generate_series(1, 10) g`, bob.ID) // ten 1 GB files, hours since last download = g
	if err := e.app.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.now(`SELECT count(*)::text FROM media WHERE deleted_at IS NULL AND size = 1073741824`, "7")
	e.now(`SELECT max(extract(hour FROM NOW() - last_access_at))::int::text FROM media WHERE deleted_at IS NULL AND size = 1073741824`, "7")
	if !e.r2.has(keep) {
		t.Fatal("LRU removed a recently used small file before older big ones")
	}
}

// TestMediaSecurityGaps covers what the media review found.
func TestMediaSecurityGaps(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	cleanup := func() {
		t.Helper()
		if err := e.app.Cleanup(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	// 1. an upload link is write-once: no swapping the file after sending
	var up upload
	e.expect(e.do("POST", "/api/media", alice.Token, map[string]any{"kind": "image", "content_type": "image/jpeg", "size": 4}), 201).json(t, &up)
	put := func(body string) int {
		req, _ := http.NewRequest("PUT", up.URL, strings.NewReader(body))
		for k, v := range up.Headers {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := put("good"); got != 200 {
		t.Fatalf("first upload: %d", got)
	}
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"to_id": bob.ID, "type": "private", "media": []string{up.ID}}), 200)
	if got := put("evil"); got != http.StatusPreconditionFailed {
		t.Fatalf("overwrite after sending: %d, want 412", got)
	}

	// 2. a deleted group's media is removed at the next cleanup
	g := e.adminGroup("pics", alice, bob)
	gm := e.uploadFile(alice, "image", "image/png", []byte("group pic"))
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"to_id": g, "type": "public", "media": []string{gm}}), 200)
	e.expect(e.do("DELETE", "/api/chats/groups/"+g, alice.Token, nil), 204)
	cleanup()
	if e.r2.has(gm) {
		t.Fatal("deleted group's media still stored")
	}

	// 3. a deleted account's media is removed too
	mine := e.uploadFile(bob, "video", "video/mp4", []byte("bob's video"))
	e.expect(e.do("POST", "/api/chats/message", bob.Token, map[string]any{"to_id": alice.ID, "type": "private", "media": []string{mine}}), 200)
	e.expect(e.do("DELETE", "/api/users/me", bob.Token, map[string]string{"password": password}), 204)
	e.expect(e.do("GET", "/api/media/"+mine, alice.Token, nil), 410)
	cleanup()
	if e.r2.has(mine) {
		t.Fatal("deleted account's media still stored")
	}
	if !e.r2.has(up.ID) {
		t.Fatal("cleanup removed media of a live account")
	}
}

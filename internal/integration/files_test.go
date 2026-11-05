//go:build integration

package integration

import (
	"bytes"
	"net/http"
	"testing"
)

func TestFilesAndLargeMedia(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	e.device(bob, "tok-bob")
	media := func(body map[string]any) resp { return e.do("POST", "/api/media", alice.Token, body) }

	// files: allowed types only, with a name that matches the type
	e.expect(media(map[string]any{"kind": "file", "content_type": "application/vnd.android.package-archive", "size": 100, "file_name": "free-coins.apk"}), 400)
	e.expect(media(map[string]any{"kind": "file", "content_type": "application/x-msdownload", "size": 100, "file_name": "setup.exe"}), 400)
	e.expect(media(map[string]any{"kind": "file", "content_type": "application/pdf", "size": 100}), 400)                       // no name
	e.expect(media(map[string]any{"kind": "file", "content_type": "application/pdf", "size": 100, "file_name": "x.apk"}), 400) // name and type disagree
	e.expect(media(map[string]any{"kind": "file", "content_type": "application/pdf", "size": 101 << 20, "file_name": "big.pdf"}), 400)
	e.expect(media(map[string]any{"kind": "image", "content_type": "image/png", "size": 100, "file_name": "x.png"}), 400) // names are for files

	// video tiers: 3 minutes when small, 10 minutes and 24 hours when large
	e.expect(media(map[string]any{"kind": "video", "content_type": "video/mp4", "size": 10 << 20, "duration_ms": 8 * 60_000}), 400)
	e.expect(media(map[string]any{"kind": "video", "content_type": "video/mp4", "size": 50 << 20, "duration_ms": 11 * 60_000}), 400)
	var large upload
	e.expect(media(map[string]any{"kind": "video", "content_type": "video/mp4", "size": 50 << 20, "duration_ms": 8 * 60_000}), 201).json(t, &large)
	var hours float64
	if err := e.db.QueryRow(`SELECT EXTRACT(EPOCH FROM expires_at - created_at) / 3600 FROM media WHERE id = $1`, large.ID).Scan(&hours); err != nil || hours != 24 {
		t.Fatalf("large video lifetime %v h (%v), want 24", hours, err)
	}
	var small upload
	e.expect(media(map[string]any{"kind": "video", "content_type": "video/mp4", "size": 10 << 20, "duration_ms": 2 * 60_000}), 201).json(t, &small)
	if err := e.db.QueryRow(`SELECT EXTRACT(EPOCH FROM expires_at - created_at) / 3600 FROM media WHERE id = $1`, small.ID).Scan(&hours); err != nil || hours != 7*24 {
		t.Fatalf("small video lifetime %v h, want 168", hours)
	}

	// a PDF end to end: the name travels with it
	var up upload
	body := []byte("%PDF-1.7 fake")
	e.expect(media(map[string]any{"kind": "file", "content_type": "application/pdf", "size": len(body), "file_name": "../รายงาน Q3.pdf"}), 201).json(t, &up)
	req, _ := http.NewRequest("PUT", up.URL, bytes.NewReader(body))
	for k, v := range up.Headers {
		req.Header.Set(k, v)
	}
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("upload: %v %v", err, res)
	}
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]any{"msg": "", "to_id": bob.ID, "type": "private", "media": []string{up.ID}}), 200)

	var page struct {
		MsgList []struct {
			Media []struct {
				Kind     string `json:"kind"`
				FileName string `json:"file_name"`
			} `json:"media"`
		} `json:"msgList"`
	}
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &page)
	if m := page.MsgList[0].Media[0]; m.Kind != "file" || m.FileName != "รายงาน Q3.pdf" {
		t.Fatalf("history shows %+v", m)
	}
	var dl struct {
		FileName string `json:"file_name"`
	}
	e.expect(e.do("GET", "/api/media/"+up.ID, bob.Token, nil), 200).json(t, &dl)
	if dl.FileName != "รายงาน Q3.pdf" {
		t.Fatalf("download name %q", dl.FileName)
	}
	// bob's phone is told what arrived
	if m := e.waitPush("tok-bob", 1)[0]; m.Body != "📎 รายงาน Q3.pdf" {
		t.Fatalf("push body %q", m.Body)
	}
}

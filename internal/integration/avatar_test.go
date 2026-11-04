//go:build integration

package integration

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"testing"
)

func (e *env) doRaw(method, path, token string, body []byte, header map[string]string) (resp, http.Header) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{code: res.StatusCode, body: raw, header: res.Header}, res.Header
}

// photoWithEXIF is a JPEG carrying an EXIF segment, like a phone photo.
func photoWithEXIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 600, 900))
	for y := range 900 {
		img.Set(300, y, color.RGBA{B: 255, A: 255})
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	exif := append([]byte{0xFF, 0xE1, 0x00, 0x1A}, []byte("Exif\x00\x00GPSLatitude=13.75N")...)
	// after the SOI marker (FFD8)
	return append(append([]byte{0xFF, 0xD8}, exif...), raw[2:]...)
}

func TestAvatar(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")

	r, _ := e.doRaw("PUT", "/api/users/me/avatar", alice.Token, []byte("hello"), nil)
	e.expect(r, 400)
	r, _ = e.doRaw("PUT", "/api/users/me/avatar", alice.Token, make([]byte, 6<<20), nil)
	e.expect(r, 413)

	r, _ = e.doRaw("PUT", "/api/users/me/avatar", alice.Token, photoWithEXIF(t), map[string]string{"Content-Type": "image/jpeg"})
	var me struct {
		AvatarURL *string `json:"avatar_url"`
	}
	e.expect(r, 200).json(t, &me)
	if me.AvatarURL == nil {
		t.Fatalf("no avatar_url: %s", r.body)
	}
	var prof struct {
		AvatarURL *string `json:"avatar_url"`
	}
	e.expect(e.do("GET", "/api/users/"+alice.ID, bob.Token, nil), 200).json(t, &prof)
	if prof.AvatarURL == nil || *prof.AvatarURL != *me.AvatarURL {
		t.Fatalf("profile avatar %v vs %v", prof.AvatarURL, *me.AvatarURL)
	}

	img, h := e.doRaw("GET", *me.AvatarURL, bob.Token, nil, nil)
	e.expect(img, 200)
	if h.Get("Content-Type") != "image/jpeg" || h.Get("ETag") == "" {
		t.Fatalf("headers %v", h)
	}
	dec, err := jpeg.Decode(bytes.NewReader(img.body))
	if err != nil || dec.Bounds().Dx() != 256 || dec.Bounds().Dy() != 256 {
		t.Fatalf("served image %v %v", dec.Bounds(), err)
	}
	if bytes.Contains(img.body, []byte("Exif")) || bytes.Contains(img.body, []byte("GPS")) {
		t.Fatal("EXIF metadata survived")
	}
	again, _ := e.doRaw("GET", *me.AvatarURL, bob.Token, nil, map[string]string{"If-None-Match": h.Get("ETag")})
	e.expect(again, 304)

	// hidden across a block
	e.expect(e.do("POST", "/api/users/blocks", alice.Token, map[string]string{"user_id": bob.ID}), 204)
	blocked, _ := e.doRaw("GET", *me.AvatarURL, bob.Token, nil, nil)
	e.expect(blocked, 404)
	e.expect(e.do("DELETE", "/api/users/blocks/"+bob.ID, alice.Token, nil), 204)

	e.expect(e.do("DELETE", "/api/users/me/avatar", alice.Token, nil), 204)
	var after struct {
		AvatarURL *string `json:"avatar_url"`
	}
	e.expect(e.do("GET", "/api/users/me", alice.Token, nil), 200).json(t, &after)
	if after.AvatarURL != nil {
		t.Fatalf("avatar_url after delete %v", *after.AvatarURL)
	}
	gone, _ := e.doRaw("GET", "/api/users/"+alice.ID+"/avatar", bob.Token, nil, nil)
	e.expect(gone, 404)
}

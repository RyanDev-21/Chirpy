//go:build integration

package integration

import (
	"RyanDev-21.com/Chirpy/internal/config"
	"strings"
	"testing"
)

func TestAvatarURLInLists(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	e.send(alice, bob.ID, "private", "hi")
	g := e.adminGroup("pics", alice, bob)
	// bob's cached friend list exists before alice's avatar does
	e.expect(e.do("GET", "/api/friends", bob.Token, nil), 200)
	r, _ := e.doRaw("PUT", "/api/users/me/avatar", alice.Token, photoWithEXIF(t), map[string]string{"Content-Type": "image/jpeg"})
	e.expect(r, 200)

	hasAvatar := func(body []byte, where string) {
		t.Helper()
		if !strings.Contains(string(body), `"avatar_url":"/api/users/`+alice.ID+`/avatar?v=`) {
			t.Errorf("%s: no avatar_url for alice: %s", where, body)
		}
	}
	hasAvatar(e.expect(e.do("GET", "/api/friends", bob.Token, nil), 200).body, "friend list (cache refreshed)")
	hasAvatar(e.expect(e.do("GET", "/api/users/search?q=alic", bob.Token, nil), 200).body, "search")
	hasAvatar(e.expect(e.do("GET", "/api/chats/groups/"+g, bob.Token, nil), 200).body, "group members")
	hasAvatar(e.expect(e.do("GET", "/api/chats", bob.Token, nil), 200).body, "conversations")

	// removing it refreshes the cached list too
	e.expect(e.do("DELETE", "/api/users/me/avatar", alice.Token, nil), 204)
	if body := e.expect(e.do("GET", "/api/friends", bob.Token, nil), 200).body; strings.Contains(string(body), "/avatar?v=") {
		t.Fatalf("stale avatar in friend list: %s", body)
	}
}

func TestLogoutAllDevices(t *testing.T) {
	e := newEnv(t)
	alice := e.signup("alice")
	var other user // a second session, like another phone
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": alice.email, "password": password}), 200).json(t, &other)
	e.device(alice, "tok-phone")
	ws := e.dial(other.Token)

	e.expect(e.do("POST", "/api/users/me/logout-all", alice.Token, nil), 204)
	for name, u := range map[string]user{"this device": alice, "other device": other} {
		e.expect(e.do("GET", "/api/users/me", u.Token, nil), 401)
		e.expect(e.do("POST", "/api/refresh", u.RefreshToken, nil), 401)
		_ = name
	}
	e.now(`SELECT count(*)::text FROM user_devices WHERE user_id = $1`, "0", alice.ID)
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("websocket stayed open after logout-all")
	}
	// signing in again works
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": alice.email, "password": password}), 200)
}

func TestRootAnswers(t *testing.T) {
	e := newEnv(t)
	r := e.expect(e.do("GET", "/", "", nil), 200)
	if !strings.Contains(string(r.body), "Chirpy API") {
		t.Fatalf("root: %s", r.body)
	}
}

func TestAppVersion(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.AppLatestVersion, c.AppMinVersion = "1.2.0", "1.0.0"
		c.AppDownloadURL, c.AppSHA256 = "https://chirpy.ponypigeon.com/chirpy-1.2.0.apk", "ab12"
	})
	var v map[string]string
	r := e.expect(e.do("GET", "/api/app/version", "", nil), 200)
	r.json(t, &v)
	if v["latest"] != "1.2.0" || v["minimum"] != "1.0.0" || v["sha256"] != "ab12" || !strings.HasPrefix(v["download_url"], "https://") {
		t.Fatalf("version %v", v)
	}
	if r.header.Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("cache %q", r.header.Get("Cache-Control"))
	}
}

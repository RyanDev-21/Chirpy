package calls

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTURNCredentials(t *testing.T) {
	var gotAuth string
	var gotTTL int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var b struct {
			TTL int `json:"ttl"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		gotTTL = b.TTL
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"iceServers":[{"urls":["stun:stun.cloudflare.com:3478","turn:turn.cloudflare.com:3478?transport=udp","turn:turn.cloudflare.com:53?transport=udp","turns:turn.cloudflare.com:443?transport=tcp"],"username":"u","credential":"c"}]}`))
	}))
	defer srv.Close()

	if NewTURN("", "tok") != nil || NewTURN("key", "") != nil {
		t.Fatal("TURN without a key must be off")
	}
	s, err := NewTURN("key", "secret").WithURL(srv.URL).ICEServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret" || gotTTL != int(TURNCredentialTTL.Seconds()) {
		t.Fatalf("request: auth %q ttl %d", gotAuth, gotTTL)
	}
	if len(s) != 1 || s[0].Username != "u" || s[0].Credential != "c" || len(s[0].URLs) != 3 || strings.Contains(strings.Join(s[0].URLs, ","), ":53?") {
		t.Fatalf("servers %+v", s)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer bad.Close()
	if _, err := NewTURN("key", "secret").WithURL(bad.URL).ICEServers(context.Background()); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("bad key: %v", err)
	}
}

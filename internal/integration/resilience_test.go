//go:build integration

package integration

import (
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/config"
)

// tcpProxy sits between the server and Redis or Postgres so a test can
// cut the connection (an outage) and restore it, while the server runs.
type tcpProxy struct {
	t      *testing.T
	ln     net.Listener
	target string
	mu     sync.Mutex
	up     bool
	conns  []net.Conn
}

func newProxy(t *testing.T, target string) *tcpProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &tcpProxy{t: t, ln: ln, target: target, up: true}
	go p.serve()
	t.Cleanup(func() { _ = ln.Close(); p.Down() })
	return p
}

func (p *tcpProxy) Addr() string { return p.ln.Addr().String() }

func (p *tcpProxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		up := p.up
		p.mu.Unlock()
		if !up {
			_ = c.Close()
			continue
		}
		s, err := net.Dial("tcp", p.target)
		if err != nil {
			_ = c.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, c, s)
		p.mu.Unlock()
		go func() { _, _ = io.Copy(s, c); _ = s.Close() }()
		go func() { _, _ = io.Copy(c, s); _ = c.Close() }()
	}
}

// Down drops every connection and refuses new ones.
func (p *tcpProxy) Down() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.up = false
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

func (p *tcpProxy) Up() {
	p.mu.Lock()
	p.up = true
	p.mu.Unlock()
}

// eventuallyCode retries a request until it answers want (the server
// reconnects on its own after an outage).
func (e *env) eventuallyCode(method, path, token string, body any, want int) resp {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		r := e.do(method, path, token, body)
		if r.code == want || time.Now().After(deadline) {
			return e.expect(r, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestRedisOutage(t *testing.T) {
	var proxy *tcpProxy
	e := newEnv(t, func(c *config.Config) {
		proxy = newProxy(t, c.RedisAddr)
		c.RedisAddr = proxy.Addr()
		c.QueueBackend = "memory"
		c.RateLimitBackend = "redis" // exercise the limiter failing open
	})
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	e.send(alice, bob.ID, "private", "before")

	proxy.Down()
	// chatting keeps working: caches fall back to Postgres, limits fail open
	e.expect(e.do("POST", "/api/chats/message", alice.Token, map[string]string{"msg": "during", "to_id": bob.ID, "type": "private"}), 200)
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200)
	e.expect(e.do("GET", "/api/friends", bob.Token, nil), 200)
	e.expect(e.do("GET", "/api/users/me", alice.Token, nil), 200)
	e.expect(e.do("GET", "/readyz", "", nil), 503)
	// "sign out everywhere" can't be guaranteed without Redis: it says so
	// rather than pretending, and works once Redis is back
	e.expect(e.do("POST", "/api/users/me/logout-all", bob.Token, nil), 500)

	proxy.Up()
	e.eventuallyCode("GET", "/readyz", "", nil, 200)
	e.expect(e.do("POST", "/api/users/me/logout-all", alice.Token, nil), 204)
	e.expect(e.do("GET", "/api/users/me", alice.Token, nil), 401)
}

func TestStartsWithoutRedis(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		proxy := newProxy(t, c.RedisAddr)
		proxy.Down()
		c.RedisAddr = proxy.Addr()
		c.QueueBackend = "memory"
		c.RateLimitBackend = "memory"
	})
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	e.send(alice, bob.ID, "private", "redis never came up")
	e.expect(e.do("GET", "/readyz", "", nil), 503)
}

func TestDatabaseOutage(t *testing.T) {
	var proxy *tcpProxy
	e := newEnv(t, func(c *config.Config) {
		u, err := url.Parse(c.DatabaseURL)
		if err != nil {
			t.Fatal(err)
		}
		proxy = newProxy(t, u.Host)
		u.Host = proxy.Addr()
		c.DatabaseURL = u.String()
	})
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)

	proxy.Down()
	// a clean error, nothing half-saved, no crash
	if r := e.do("POST", "/api/chats/message", alice.Token, map[string]string{"msg": "lost", "to_id": bob.ID, "type": "private"}); r.code != 500 {
		t.Fatalf("send during outage: %d %s", r.code, r.body)
	}
	e.expect(e.do("GET", "/readyz", "", nil), 503)
	e.expect(e.do("GET", "/livez", "", nil), 204) // the process stays up

	proxy.Up()
	e.eventuallyCode("GET", "/readyz", "", nil, 200)
	e.send(alice, bob.ID, "private", "back")
	var p page
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &p)
	if got := p.texts(); len(got) != 1 || got[0] != "back" {
		t.Fatalf("history after the outage: %v", got)
	}
}

func TestPushProviderDown(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	e.device(bob, "tok-bob")
	e.push.mu.Lock()
	e.push.down = true
	e.push.mu.Unlock()
	// the message is saved and delivered; only the notification is lost
	e.send(alice, bob.ID, "private", "you there?")
	var p page
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &p)
	if len(p.texts()) != 1 {
		t.Fatalf("history %v", p.texts())
	}
	e.now(`SELECT count(*)::text FROM user_devices WHERE token = 'tok-bob'`, "1") // not mistaken for a dead token

	e.push.mu.Lock()
	e.push.down = false
	e.push.mu.Unlock()
	e.send(alice, bob.ID, "private", "now?")
	if m := e.waitPush("tok-bob", 1)[0]; m.Body != "now?" {
		t.Fatalf("after recovery got %q", m.Body)
	}
}

func TestRetriedSendIsNotDuplicated(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	body := map[string]string{"msg": "only once", "to_id": bob.ID, "type": "private", "client_msg_id": "5f0c6a1e-8a2b-4c3d-9e4f-0a1b2c3d4e5f"}
	var first, again struct {
		MsgID string `json:"msg_id"`
	}
	e.expect(e.do("POST", "/api/chats/message", alice.Token, body), 200).json(t, &first)
	e.expect(e.do("POST", "/api/chats/message", alice.Token, body), 200).json(t, &again)
	if first.MsgID == "" || first.MsgID != again.MsgID {
		t.Fatalf("retry got %q, first %q", again.MsgID, first.MsgID)
	}
	var p page
	e.expect(e.do("GET", "/api/chat/"+alice.ID+"/messages", bob.Token, nil), 200).json(t, &p)
	if len(p.texts()) != 1 {
		t.Fatalf("history %v", p.texts())
	}
	body["client_msg_id"] = "not-a-uuid"
	e.expect(e.do("POST", "/api/chats/message", alice.Token, body), 400)
}

func TestSeenMarkerOnlyMovesForward(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	older := e.send(alice, bob.ID, "private", "one")
	newer := e.send(alice, bob.ID, "private", "two")
	seen := "/api/chat/" + alice.ID + "/messages"
	e.expect(e.do("POST", seen, bob.Token, map[string]string{"msg_id": newer}), 201)
	e.eventually(`SELECT message_id::text FROM seen_messages WHERE seen_id = $1`, newer, bob.ID)
	// a late duplicate receipt for an older message arrives afterwards
	e.expect(e.do("POST", seen, bob.Token, map[string]string{"msg_id": older}), 201)
	time.Sleep(500 * time.Millisecond)
	e.now(`SELECT message_id::text FROM seen_messages WHERE seen_id = $1`, newer, bob.ID)
}

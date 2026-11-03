//go:build integration

// Package integration runs the whole server against real Postgres and Redis.
//
//	TEST_DATABASE_URL=postgres://... TEST_REDIS_ADDR=localhost:6379 \
//	  go test -tags integration -race ./internal/integration/...
//
// Each test truncates every table and flushes the Redis DB, so point these
// at throwaway instances.
package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/app"
	"RyanDev-21.com/Chirpy/internal/config"
	"RyanDev-21.com/Chirpy/internal/platform/migrate"
	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
)

// fixtures are generated per run so nothing credential-shaped is committed
var (
	secret   = uuid.NewString() + uuid.NewString()
	password = "Pw-" + uuid.NewString()[:8] + "!"
)

var (
	dbURL     = os.Getenv("TEST_DATABASE_URL")
	redisAddr = os.Getenv("TEST_REDIS_ADDR")
)

func TestMain(m *testing.M) {
	if dbURL == "" || redisAddr == "" {
		if os.Getenv("CI") != "" {
			panic("TEST_DATABASE_URL and TEST_REDIS_ADDR must be set in CI")
		}
		os.Stdout.WriteString("skipping integration tests: TEST_DATABASE_URL / TEST_REDIS_ADDR not set\n")
		os.Exit(0)
	}
	// the same embedded migrations the server applies on start
	if err := migrate.Up(context.Background(), dbURL, slog.New(slog.DiscardHandler)); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

type env struct {
	t      *testing.T
	srv    *httptest.Server
	db     *sql.DB
	redis  *redis.Client
	cancel context.CancelFunc
}

// newEnv resets Postgres and Redis and starts a fresh server with workers.
// opts adjust the config before the server starts.
func newEnv(t *testing.T, opts ...func(*config.Config)) *env {
	t.Helper()
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`TRUNCATE users, refresh_tokens, user_relationships, message, chat_groups,
		GroupMessage, member_table, EleConfig, seen_messages CASCADE`); err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Addr:            ":0",
		Platform:        "test",
		DatabaseURL:     dbURL,
		RedisAddr:       redisAddr,
		JWTSecret:       secret,
		ShutdownTimeout: time.Second,
		QueueBackend:    "redis",
	}
	for _, o := range opts {
		o(&cfg)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("CHIRPY_TEST_LOG") != "" {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	ctx, cancel := context.WithCancel(context.Background())
	a, err := app.New(ctx, cfg, logger)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := a.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Router())
	e := &env{t: t, srv: srv, db: db, redis: rdb, cancel: cancel}
	t.Cleanup(func() {
		srv.Close()
		cancel()
		a.Close()
		_ = rdb.Close()
		_ = db.Close()
	})
	return e
}

type resp struct {
	code int
	body []byte
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (e *env) do(method, path, token string, body any) resp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{code: res.StatusCode, body: raw}
}

func (e *env) expect(r resp, code int) resp {
	e.t.Helper()
	if r.code != code {
		e.t.Fatalf("status = %d, want %d: %s", r.code, code, r.body)
	}
	return r
}

type user struct {
	ID           string `json:"id"`
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	email        string
}

func (e *env) signup(name string) user {
	e.t.Helper()
	email := name + "@example.io"
	e.expect(e.do("POST", "/api/users", "", map[string]string{"name": name, "email": email, "password": password}), 201)
	var u user
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": email, "password": password}), 200).json(e.t, &u)
	u.email = email
	return u
}

// now asserts a SQL query returns want (as text) right away: for writes
// that must be committed before the API responds.
func (e *env) now(query, want string, args ...any) {
	e.t.Helper()
	var got string
	if err := e.db.QueryRow(query, args...).Scan(&got); err != nil || got != want {
		e.t.Fatalf("%s = %q (err %v), want %q", query, got, err, want)
	}
}

// seedUser creates a user directly in postgres and mints a token, for
// tests that need many users without hitting the signup rate limit.
func (e *env) seedUser(name string) user {
	e.t.Helper()
	var id string
	if err := e.db.QueryRow(`INSERT INTO users (name, email, password) VALUES ($1, $2, 'unused') RETURNING id`,
		name, name+"@example.io").Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	token, err := secure.MakeJWT(uuid.MustParse(id), secret, time.Hour)
	if err != nil {
		e.t.Fatal(err)
	}
	return user{ID: id, Token: token, email: name + "@example.io"}
}

// invite returns a group's invite code as seen by a member.
func (e *env) invite(token, groupID string) string {
	e.t.Helper()
	var c struct {
		Code string `json:"code"`
	}
	e.expect(e.do("GET", "/api/chats/groups/"+groupID+"/invite", token, nil), 200).json(e.t, &c)
	return c.Code
}

// join joins a group with an invite code.
func (e *env) join(token, code string) resp {
	e.t.Helper()
	return e.do("POST", "/api/chats/groups/join", token, map[string]string{"code": code})
}

// eventually polls a SQL query until it returns want (as text).
func (e *env) eventually(query, want string, args ...any) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		if err := e.db.QueryRow(query, args...).Scan(&got); err == nil && got == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatalf("%s = %q, want %q", query, got, want)
}

func (e *env) dial(token string) *websocket.Conn {
	e.t.Helper()
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/api/chats/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = conn.Close() })
	time.Sleep(50 * time.Millisecond) // let the hub register the client
	return conn
}

type wsEvent struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

func readEvent(t *testing.T, conn *websocket.Conn) wsEvent {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("websocket read: %v", err)
	}
	var ev wsEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("bad event %s", raw)
	}
	return ev
}

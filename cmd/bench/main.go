// Command bench load-tests a running server (see bench/docker-compose.yml):
// it seeds users, friendships, groups and history straight into postgres,
// then runs each user action for a while and reports latency, throughput,
// and how many postgres queries and redis commands each action costs.
//
//	BENCH_SECRET=... go run ./cmd/bench -out bench/results.md
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"RyanDev-21.com/Chirpy/internal/groups"
	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var (
	api       = flag.String("api", "http://127.0.0.1:57080", "server base URL")
	dbURL     = flag.String("db", "postgres://chirpy:chirpy@127.0.0.1:57432/bench?sslmode=disable", "postgres (for seeding and query counts)")
	redisAddr = flag.String("redis", "127.0.0.1:57379", "redis (for command counts)")
	nUsers    = flag.Int("users", 300, "seeded users")
	duration  = flag.Duration("d", 15*time.Second, "time per scenario")
	conc      = flag.Int("c", 10, "concurrent clients per scenario")
	outFile   = flag.String("out", "", "also write the markdown report here")
	label     = flag.String("label", "Render free (0.1 CPU, 512 MB)", "report heading")
)

const (
	friendsPerUser = 5
	nGroups        = 20
	groupSize      = 15
	historyPerPair = 60
	historyGroup   = 300
)

type user struct {
	id    uuid.UUID
	token string
}

type world struct {
	users   []user
	friends map[uuid.UUID][]uuid.UUID
	groups  [][]int // member indexes; [0] is the leader
	groupID []uuid.UUID
	pairMsg map[[2]uuid.UUID]uuid.UUID // an older message per pair, for paging
}

func main() {
	flag.Parse()
	secret := os.Getenv("BENCH_SECRET")
	if secret == "" {
		log.Fatal("set BENCH_SECRET to the server's SECRET")
	}
	ctx := context.Background()
	waitReady()
	pool, err := pgxpool.New(ctx, *dbURL)
	must(err)
	defer pool.Close()
	rdb := redis.NewClient(&redis.Options{Addr: *redisAddr})
	w := seed(ctx, pool, secret)
	log.Printf("seeded %d users, %d groups", len(w.users), len(w.groupID))

	var report strings.Builder
	fmt.Fprintf(&report, "## %s\n\n%d users, %d concurrent clients, %s per action.\n\n", *label, *nUsers, *conc, *duration)
	idleRedis := idleCommands(ctx, rdb)
	fmt.Fprintf(&report, "Idle background load: %.1f redis commands/second.\n\n", idleRedis)
	fmt.Fprintln(&report, "| Action | Requests | Errors | 429s | req/s | p50 | p95 | p99 | PG queries/op | Redis cmds/op |")
	fmt.Fprintln(&report, "|---|---|---|---|---|---|---|---|---|---|")

	run := func(name string, op func(c *client, i int) int) {
		r := scenario(ctx, pool, rdb, w, op)
		line := fmt.Sprintf("| %s | %d | %d | %d | %.0f | %s | %s | %s | %.1f | %.1f |",
			name, r.n, r.errs, r.limited, r.rps, ms(r.p50), ms(r.p95), ms(r.p99), r.pgPerOp, r.redisPerOp-idleRedis*duration.Seconds()/float64(max(r.n, 1)))
		fmt.Fprintln(&report, line)
		log.Println(line)
	}

	run("GET /users/me", func(c *client, i int) int { return c.get("/api/users/me") })
	run("GET /chats (conversation list)", func(c *client, i int) int { return c.get("/api/chats") })
	run("GET /friends (friend list)", func(c *client, i int) int { return c.get("/api/friends") })
	privHistory := func(c *client, i int) int {
		return c.get("/api/chat/" + w.friends[c.u.id][i%friendsPerUser].String() + "/messages")
	}
	run("GET private history (first read: fills cache)", privHistory)
	run("GET private history (cached)", privHistory)
	run("GET older history (?before=)", func(c *client, i int) int {
		f := w.friends[c.u.id][i%friendsPerUser]
		return c.get("/api/chat/" + f.String() + "/messages?before=" + w.pairMsg[pair(c.u.id, f)].String())
	})
	groupHistory := func(c *client, i int) int {
		g := w.groupOf(c.idx)
		if g < 0 {
			return c.get("/api/users/me")
		}
		return c.get("/api/chat/groups/" + w.groupID[g].String() + "/messages")
	}
	run("GET group history (first read)", groupHistory)
	run("GET group history (cached)", groupHistory)
	run("POST private message", func(c *client, i int) int {
		return c.post("/api/chats/message", map[string]string{"msg": "bench " + strconv.Itoa(i), "to_id": w.friends[c.u.id][i%friendsPerUser].String(), "type": "private"})
	})
	run("POST group message (15 members)", func(c *client, i int) int {
		g := w.groupOf(c.idx)
		if g < 0 {
			return c.get("/api/users/me")
		}
		return c.post("/api/chats/message", map[string]string{"msg": "bench " + strconv.Itoa(i), "to_id": w.groupID[g].String(), "type": "public"})
	})
	run("PUT reaction", func(c *client, i int) int {
		m := w.pairMsg[pair(c.u.id, w.friends[c.u.id][i%friendsPerUser])]
		return c.do("PUT", "/api/chat/messages/"+m.String()+"/reaction", map[string]string{"emoji": "👍"})
	})
	run("GET /users/search", func(c *client, i int) int { return c.get("/api/users/search?q=bench" + strconv.Itoa(i%9)) })
	run("GET /discover", func(c *client, i int) int { return c.get("/api/discover") })

	resetLimits(ctx, rdb)
	fanout := wsFanout(ctx, w)
	resetLimits(ctx, rdb)
	fmt.Fprintf(&report, "\nWebSocket fan-out: %s\n", fanout)
	log.Println("websocket:", fanout)
	login := loginLatency()
	fmt.Fprintf(&report, "\nLogin (argon2 password check, sequential, 8 tries; the 10/min limit stops more): %s\n", login)
	log.Println("login:", login)

	fmt.Println(report.String())
	if *outFile != "" {
		must(os.WriteFile(*outFile, []byte(report.String()), 0o644))
	}
}

// --- seeding ---

const benchPassword = "Bench-pass!1"

func seed(ctx context.Context, pool *pgxpool.Pool, secret string) *world {
	_, err := pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
		TRUNCATE users, user_relationships, chat_groups, message, GroupMessage, member_table CASCADE`)
	must(err)
	hash, err := secure.HashPassword(benchPassword)
	must(err)
	w := &world{friends: map[uuid.UUID][]uuid.UUID{}, pairMsg: map[[2]uuid.UUID]uuid.UUID{}}
	places := []string{"th-bangkok", "th-chiang-mai", "mm-yangon", "mm-mandalay", "europe"}
	interests := []string{"gaming", "football", "music", "kpop", "anime", "coding", "travel", "food"}
	for i := range *nUsers {
		id := uuid.Must(uuid.NewV7())
		place := places[i%len(places)]
		_, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password, email_verified_at, discoverable, location_id, location_country)
			VALUES ($1, $2, $3, $4, NOW(), true, $5, $6)`,
			id, fmt.Sprintf("bench%04d", i), fmt.Sprintf("bench%04d@example.io", i), hash, place, strings.SplitN(place, "-", 2)[0])
		must(err)
		for k := range 3 {
			_, err := pool.Exec(ctx, `INSERT INTO user_interests VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, interests[(i+k*3)%len(interests)])
			must(err)
		}
		tok, err := secure.MakeJWT(id, secret, 2*time.Hour)
		must(err)
		w.users = append(w.users, user{id: id, token: tok})
	}
	// ring of friendships: i is friends with i+1..i+5
	for i, u := range w.users {
		for k := 1; k <= friendsPerUser; k++ {
			v := w.users[(i+k)%len(w.users)]
			_, err := pool.Exec(ctx, `INSERT INTO user_relationships (id, user_id, otherUser_id, status) VALUES (gen_random_uuid(), $1, $2, 'confirm')`, u.id, v.id)
			must(err)
			w.friends[u.id] = append(w.friends[u.id], v.id)
			w.friends[v.id] = append(w.friends[v.id], u.id)
		}
	}
	// history for every pair, oldest first; remember one message mid-way
	for a, fs := range w.friends {
		for _, b := range fs {
			if a.String() > b.String() {
				continue
			}
			var mid uuid.UUID
			must(pool.QueryRow(ctx, `WITH ins AS (
				INSERT INTO message (id, content, from_id, to_id, created_at)
				SELECT gen_random_uuid(), 'history ' || g, CASE WHEN g % 2 = 0 THEN $1::uuid ELSE $2::uuid END,
				       CASE WHEN g % 2 = 0 THEN $2::uuid ELSE $1::uuid END, NOW() - (g || ' minutes')::interval
				FROM generate_series(1, $3::int) g RETURNING id, created_at)
				SELECT id FROM ins ORDER BY created_at LIMIT 1 OFFSET 10`, a, b, historyPerPair).Scan(&mid))
			w.pairMsg[pair(a, b)] = mid
		}
	}
	for g := range nGroups {
		gid := uuid.Must(uuid.NewV7())
		code, err := groups.NewInviteCode()
		must(err)
		_, err = pool.Exec(ctx, `INSERT INTO chat_groups (id, name, description, max_member, current_member, invite_code) VALUES ($1, $2, '', 50, $3, $4)`,
			gid, fmt.Sprintf("bench-group-%d", g), groupSize, code)
		must(err)
		var members []int
		for m := range groupSize {
			idx := (g*groupSize + m) % len(w.users)
			role := "member"
			if m == 0 {
				role = "Leader"
			}
			_, err := pool.Exec(ctx, `INSERT INTO member_table (group_id, member_id, role, joined_at) VALUES ($1, $2, $3, NOW() - interval '30 days')`, gid, w.users[idx].id, role)
			must(err)
			members = append(members, idx)
		}
		_, err = pool.Exec(ctx, `INSERT INTO GroupMessage (id, content, group_id, from_id, created_at)
			SELECT gen_random_uuid(), 'group history ' || g, $1, $2, NOW() - (g || ' minutes')::interval FROM generate_series(1, $3::int) g`,
			gid, w.users[members[0]].id, historyGroup)
		must(err)
		w.groups, w.groupID = append(w.groups, members), append(w.groupID, gid)
	}
	_, err = pool.Exec(ctx, `ANALYZE`)
	must(err)
	return w
}

func (w *world) groupOf(idx int) int {
	for g, ms := range w.groups {
		if slices.Contains(ms, idx) {
			return g
		}
	}
	return -1
}

func pair(a, b uuid.UUID) [2]uuid.UUID {
	if a.String() > b.String() {
		a, b = b, a
	}
	return [2]uuid.UUID{a, b}
}

// --- running a scenario ---

type client struct {
	u   user
	idx int
}

var httpc = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 64}}

func (c *client) do(method, path string, body any) int {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, *api+path, rd)
	req.Header.Set("Authorization", "Bearer "+c.u.token)
	res, err := httpc.Do(req)
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode
}
func (c *client) get(path string) int            { return c.do("GET", path, nil) }
func (c *client) post(path string, body any) int { return c.do("POST", path, body) }

type result struct {
	n, errs, limited    int
	rps                 float64
	p50, p95, p99       time.Duration
	pgPerOp, redisPerOp float64
}

func scenario(ctx context.Context, pool *pgxpool.Pool, rdb *redis.Client, w *world, op func(c *client, i int) int) result {
	resetLimits(ctx, rdb)
	_, _ = pool.Exec(ctx, `SELECT pg_stat_statements_reset()`)
	_ = rdb.ConfigResetStat(ctx).Err()
	var (
		mu        sync.Mutex
		lat       []time.Duration
		errs, lim atomic.Int64
		next      atomic.Int64
	)
	deadline := time.Now().Add(*duration)
	var wg sync.WaitGroup
	for range *conc {
		wg.Go(func() {
			for time.Now().Before(deadline) {
				i := int(next.Add(1))
				idx := i % len(w.users) // spread over users: limits are per user
				c := &client{u: w.users[idx], idx: idx}
				start := time.Now()
				code := op(c, i)
				d := time.Since(start)
				switch {
				case code == http.StatusTooManyRequests:
					lim.Add(1)
				case code < 200 || code >= 300:
					errs.Add(1)
				}
				mu.Lock()
				lat = append(lat, d)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	n := len(lat)
	slices.Sort(lat)
	pct := func(p float64) time.Duration {
		if n == 0 {
			return 0
		}
		return lat[min(n-1, int(float64(n)*p))]
	}
	var pgCalls float64
	_ = pool.QueryRow(ctx, `SELECT COALESCE(sum(calls), 0)::float8 FROM pg_stat_statements
		WHERE query NOT ILIKE '%pg_stat_statements%' AND query NOT ILIKE '%pg_catalog%'
		  AND dbid = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&pgCalls)
	return result{
		n: n, errs: int(errs.Load()), limited: int(lim.Load()), rps: float64(n) / duration.Seconds(),
		p50: pct(0.50), p95: pct(0.95), p99: pct(0.99),
		pgPerOp: pgCalls / float64(max(n, 1)), redisPerOp: float64(redisCalls(ctx, rdb)) / float64(max(n, 1)),
	}
}

// redisCalls is how many commands clients sent since the last reset (a
// script counts once, as Upstash bills it), minus the benchmark's own.
func redisCalls(ctx context.Context, rdb *redis.Client) int64 {
	info, err := rdb.Info(ctx, "stats", "commandstats").Result()
	if err != nil {
		return 0
	}
	var total, own int64
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "total_commands_processed:"); ok {
			total, _ = strconv.ParseInt(v, 10, 64)
		}
		name, rest, ok := strings.Cut(line, ":calls=")
		if ok && (name == "cmdstat_info" || strings.HasPrefix(name, "cmdstat_config") || name == "cmdstat_scan" || name == "cmdstat_del") {
			v, _ := strconv.ParseInt(strings.SplitN(rest, ",", 2)[0], 10, 64)
			own += v
		}
	}
	return total - own
}

// resetLimits starts fresh rate-limit windows; caches stay warm.
func resetLimits(ctx context.Context, rdb *redis.Client) {
	iter := rdb.Scan(ctx, 0, "rl:*", 1000).Iterator()
	for iter.Next(ctx) {
		_ = rdb.Del(ctx, iter.Val()).Err()
	}
}

func idleCommands(ctx context.Context, rdb *redis.Client) float64 {
	_ = rdb.ConfigResetStat(ctx).Err()
	time.Sleep(5 * time.Second)
	return float64(redisCalls(ctx, rdb)) / 5
}

// --- websocket fan-out ---

func wsFanout(ctx context.Context, w *world) string {
	const groupsUsed = 5
	var received atomic.Int64
	var mu sync.Mutex
	var lat []time.Duration
	var conns []*websocket.Conn
	for g := range groupsUsed {
		for _, idx := range w.groups[g][1:] {
			h := http.Header{"Authorization": {"Bearer " + w.users[idx].token}}
			conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(*api, "http", "ws", 1)+"/api/chats/ws", h)
			if err != nil {
				return "dial failed: " + err.Error()
			}
			conns = append(conns, conn)
			go func() {
				for {
					_, raw, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var ev struct {
						Event   string `json:"event"`
						Payload struct {
							Msg string `json:"msg"`
						} `json:"payload"`
					}
					if json.Unmarshal(raw, &ev) != nil || ev.Event != "msg" {
						continue
					}
					if sent, err := strconv.ParseInt(strings.TrimPrefix(ev.Payload.Msg, "t="), 10, 64); err == nil {
						mu.Lock()
						lat = append(lat, time.Since(time.Unix(0, sent)))
						mu.Unlock()
						received.Add(1)
					}
				}
			}()
		}
	}
	time.Sleep(time.Second)
	sent := 0
	deadline := time.Now().Add(*duration)
	for time.Now().Before(deadline) {
		for g := range groupsUsed {
			leader := &client{u: w.users[w.groups[g][0]]}
			if leader.post("/api/chats/message", map[string]string{"msg": "t=" + strconv.FormatInt(time.Now().UnixNano(), 10), "to_id": w.groupID[g].String(), "type": "public"}) == 200 {
				sent++
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)
	for _, c := range conns {
		_ = c.Close()
	}
	slices.Sort(lat)
	n := len(lat)
	if n == 0 {
		return "no deliveries"
	}
	return fmt.Sprintf("%d sockets, %d group messages sent, %d of %d deliveries arrived; send-to-receive p50 %s, p95 %s, p99 %s",
		len(conns), sent, received.Load(), sent*(groupSize-1), ms(lat[n/2]), ms(lat[n*95/100]), ms(lat[min(n-1, n*99/100)]))
}

func loginLatency() string {
	var lat []time.Duration
	for i := range 8 {
		body, _ := json.Marshal(map[string]string{"email": fmt.Sprintf("bench%04d@example.io", i), "password": benchPassword})
		start := time.Now()
		res, err := httpc.Post(*api+"/api/login", "application/json", bytes.NewReader(body))
		if err != nil {
			return err.Error()
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			return "login returned " + res.Status
		}
		lat = append(lat, time.Since(start))
	}
	slices.Sort(lat)
	return fmt.Sprintf("p50 %s, max %s", ms(lat[4]), ms(lat[7]))
}

// --- helpers ---

func waitReady() {
	for range 120 {
		if res, err := http.Get(*api + "/livez"); err == nil {
			res.Body.Close()
			if res.StatusCode == 204 {
				return
			}
		}
		time.Sleep(time.Second)
	}
	log.Fatal("server did not become ready")
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/admin"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/jackc/pgx/v5/pgxpool"
)

// grantAdmin uses the same code path as cmd/admin.
func (e *env) grantAdmin(u user) {
	e.t.Helper()
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		e.t.Fatal(err)
	}
	defer pool.Close()
	svc := admin.NewService(pool, users.NewUserRepo(database.New(pool)), nil, testLogger())
	if _, err := svc.SetAdmin(context.Background(), u.email, true); err != nil {
		e.t.Fatal(err)
	}
}

// adminToken signs in to the admin page: password, then the emailed code.
func (e *env) adminToken(u user) string {
	e.t.Helper()
	e.expect(e.do("POST", "/api/admin/login", "", map[string]string{"email": u.email, "password": password}), 202)
	var r struct {
		Token string `json:"token"`
	}
	e.expect(e.do("POST", "/api/admin/verify", "", map[string]string{"email": u.email, "code": e.lastCode(u.email)}), 200).json(e.t, &r)
	return r.Token
}

func TestAdminSignIn(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	// not an admin yet: same answer as a wrong password
	e.expect(e.do("POST", "/api/admin/login", "", map[string]string{"email": alice.email, "password": password}), 401)
	e.grantAdmin(alice)
	e.expect(e.do("POST", "/api/admin/login", "", map[string]string{"email": alice.email, "password": "wrong-" + password}), 401)
	e.expect(e.do("POST", "/api/admin/login", "", map[string]string{"email": bob.email, "password": password}), 401)
	e.expect(e.do("POST", "/api/admin/login", "", map[string]string{"email": alice.email, "password": password}), 202)
	e.expect(e.do("POST", "/api/admin/verify", "", map[string]string{"email": alice.email, "code": "000000"}), 400)
	tok := e.adminToken(alice)

	// admin and app tokens don't work on each other's API
	e.expect(e.do("GET", "/api/admin/overview", alice.Token, nil), 401)
	e.expect(e.do("GET", "/api/users/me", tok, nil), 401)

	var o map[string]any
	e.expect(e.do("GET", "/api/admin/overview", tok, nil), 200).json(t, &o)
	if o["users"].(float64) != 2 {
		t.Fatalf("overview %v", o)
	}
	r := e.expect(e.do("GET", "/api/admin/users?q=bobby", tok, nil), 200)
	if !strings.Contains(string(r.body), bob.email) {
		t.Fatalf("user search %s", r.body)
	}
	if strings.Contains(string(r.body), "password") {
		t.Fatalf("user search leaks secrets: %s", r.body)
	}
	e.now(`SELECT count(*)::text FROM admin_actions WHERE action = 'search_users' AND detail = 'bobby'`, "1")

	// revoking admin rights cuts off an open admin session at once
	if _, err := e.db.Exec(`UPDATE users SET is_admin = false WHERE id = $1`, alice.ID); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("GET", "/api/admin/overview", tok, nil), 403)

	// the page is served with a strict policy
	page := e.do("GET", "/admin/", "", nil)
	e.expect(page, 200)
	if !strings.Contains(page.header.Get("Content-Security-Policy"), "script-src 'self'") || !strings.Contains(string(page.body), "Chirpy Admin") {
		t.Fatalf("admin page: %v %s", page.header, page.body[:min(200, len(page.body))])
	}
}

func TestBanAndUnban(t *testing.T) {
	e := newEnv(t)
	mod, bob, carol, dave := e.signup("mod"), e.signup("bobby"), e.signup("carol"), e.signup("dave")
	e.grantAdmin(mod)
	e.grantAdmin(dave)
	tok := e.adminToken(mod)
	e.befriend(bob, carol)
	e.send(bob, carol.ID, "private", "spam spam")
	g := e.adminGroup("club", bob, carol) // bob leads
	ban := func(target user, body map[string]any) resp {
		return e.do("POST", "/api/admin/users/"+target.ID+"/ban", tok, body)
	}
	e.expect(ban(mod, map[string]any{"reason": "x"}), 400)  // yourself
	e.expect(ban(dave, map[string]any{"reason": "x"}), 400) // another admin
	e.expect(ban(bob, map[string]any{"reason": ""}), 400)   // reason required
	e.expect(ban(bob, map[string]any{"reason": "spam", "until": time.Now().Add(7 * 24 * time.Hour)}), 204)

	// signed out everywhere, can't come back
	e.expect(e.do("GET", "/api/users/me", bob.Token, nil), 401)
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": bob.email, "password": password}), 403)
	// hidden from others
	if strings.Contains(string(e.expect(e.do("GET", "/api/users/search?q=bobb", carol.Token, nil), 200).body), bob.ID) {
		t.Fatal("banned user still in search")
	}
	e.expect(e.do("GET", "/api/users/"+bob.ID, carol.Token, nil), 404)
	// removed from the group; carol leads now
	e.now(`SELECT count(*)::text FROM member_table WHERE group_id = $1 AND member_id = $2`, "0", g, bob.ID)
	e.now(`SELECT role FROM member_table WHERE group_id = $1 AND member_id = $2`, "Leader", g, carol.ID)
	e.now(`SELECT current_member::text FROM chat_groups WHERE id = $1`, "1", g)
	// past messages hidden, not deleted
	var page struct {
		MsgList []struct {
			Suspended bool `json:"sender_suspended"`
			MsgInfo   struct {
				Msg struct {
					Content string `json:"msg"`
				} `json:"Msg"`
			} `json:"message_info"`
		} `json:"msgList"`
	}
	e.expect(e.do("GET", "/api/chat/"+bob.ID+"/messages", carol.Token, nil), 200).json(t, &page)
	if len(page.MsgList) != 1 || !page.MsgList[0].Suspended || page.MsgList[0].MsgInfo.Msg.Content != "" {
		t.Fatalf("banned sender's message %+v", page.MsgList)
	}
	e.now(`SELECT content FROM message WHERE from_id = $1`, "spam spam", bob.ID)

	// unban restores the messages and the login (not the group)
	e.expect(e.do("DELETE", "/api/admin/users/"+bob.ID+"/ban", tok, nil), 204)
	e.expect(e.do("DELETE", "/api/admin/users/"+bob.ID+"/ban", tok, nil), 400)
	e.expect(e.do("GET", "/api/chat/"+bob.ID+"/messages", carol.Token, nil), 200).json(t, &page)
	if page.MsgList[0].Suspended || page.MsgList[0].MsgInfo.Msg.Content != "spam spam" {
		t.Fatalf("after unban %+v", page.MsgList)
	}
	e.expect(e.do("POST", "/api/login", "", map[string]string{"email": bob.email, "password": password}), 200)

	// a temporary ban lifts itself in the hourly cleanup
	e.expect(ban(bob, map[string]any{"reason": "cool off", "until": time.Now().Add(time.Hour)}), 204)
	if _, err := e.db.Exec(`UPDATE users SET banned_until = NOW() - interval '1 minute' WHERE id = $1`, bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.now(`SELECT (banned_at IS NULL)::text FROM users WHERE id = $1`, "true", bob.ID)
	for _, action := range []string{"ban", "unban", "ban_expired", "ban_removed_from_group"} {
		e.now(`SELECT (count(*) > 0)::text FROM admin_actions WHERE action = $1 AND target_user = $2`, "true", action, bob.ID)
	}
	var audit struct {
		Actions []struct {
			Action string `json:"action"`
		} `json:"actions"`
	}
	e.expect(e.do("GET", "/api/admin/audit", tok, nil), 200).json(t, &audit)
	if len(audit.Actions) < 4 {
		t.Fatalf("audit %+v", audit)
	}
}

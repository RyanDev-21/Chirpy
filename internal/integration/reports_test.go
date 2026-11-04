//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestReportUser(t *testing.T) {
	e := newEnv(t)
	mod, alice, bob, carol := e.signup("mod"), e.signup("alice"), e.signup("bobby"), e.signup("carol")
	e.grantAdmin(mod)
	e.befriend(carol, bob)
	e.befriend(alice, bob)
	abuse := e.send(bob, carol.ID, "private", "you are awful")
	reply := e.send(carol, bob.ID, "private", "please stop")
	notCarols := e.send(bob, alice.ID, "private", "private to alice")
	g := e.adminGroup("club", carol, bob)
	inGroup := e.send(bob, g, "public", "group abuse")
	other := e.adminGroup("other", alice, bob)
	notCarolsGroup := e.send(bob, other, "public", "carol isn't here")

	report := func(body map[string]any) resp { return e.do("POST", "/api/reports", carol.Token, body) }
	e.expect(report(map[string]any{"user_id": carol.ID, "reason": "spam"}), 400) // yourself
	e.expect(report(map[string]any{"user_id": bob.ID, "reason": "boring"}), 400) // not a reason
	e.expect(report(map[string]any{"user_id": "00000000-0000-0000-0000-000000000001", "reason": "spam"}), 404)
	e.expect(report(map[string]any{"user_id": bob.ID, "reason": "harassment", "messages": []string{notCarols}}), 400)      // not her chat
	e.expect(report(map[string]any{"user_id": bob.ID, "reason": "harassment", "messages": []string{notCarolsGroup}}), 400) // not her group

	var r struct {
		ID string `json:"report_id"`
	}
	e.expect(report(map[string]any{"user_id": bob.ID, "reason": "harassment", "note": "keeps messaging me",
		"messages": []string{abuse, reply, inGroup}}), 201).json(t, &r)
	// reporting again updates the same open report
	var again struct {
		ID string `json:"report_id"`
	}
	e.expect(report(map[string]any{"user_id": bob.ID, "reason": "harassment", "note": "still"}), 201).json(t, &again)
	if again.ID != r.ID {
		t.Fatalf("second report opened a new one: %s vs %s", again.ID, r.ID)
	}
	e.now(`SELECT count(*)::text FROM reports`, "1")

	// the admin sees exactly the attached messages, nothing else
	tok := e.adminToken(mod)
	var o map[string]any
	e.expect(e.do("GET", "/api/admin/overview", tok, nil), 200).json(t, &o)
	if o["open_reports"].(float64) != 1 {
		t.Fatalf("overview %v", o)
	}
	list := string(e.expect(e.do("GET", "/api/admin/reports?status=open", tok, nil), 200).body)
	if !strings.Contains(list, r.ID) || !strings.Contains(list, `"reported_name":"bobby"`) {
		t.Fatalf("report list %s", list)
	}
	detail := string(e.expect(e.do("GET", "/api/admin/reports/"+r.ID, tok, nil), 200).body)
	for _, want := range []string{"you are awful", "please stop", "group abuse", "still"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail lacks %q: %s", want, detail)
		}
	}
	for _, never := range []string{"private to alice", "carol isn't here"} {
		if strings.Contains(detail, never) {
			t.Fatalf("detail leaks %q", never)
		}
	}
	e.now(`SELECT count(*)::text FROM admin_actions WHERE action = 'view_report'`, "1")
	// evidence survives the sender deleting the message
	e.expect(e.do("DELETE", "/api/chat/messages/"+abuse, bob.Token, nil), 204)
	if !strings.Contains(string(e.expect(e.do("GET", "/api/admin/reports/"+r.ID, tok, nil), 200).body), "you are awful") {
		t.Fatal("evidence lost when the message was deleted")
	}

	// app tokens can't read reports
	e.expect(e.do("GET", "/api/admin/reports", carol.Token, nil), 401)

	e.expect(e.do("POST", "/api/admin/reports/"+r.ID+"/resolve", tok, map[string]string{"status": "maybe"}), 400)
	e.expect(e.do("POST", "/api/admin/reports/"+r.ID+"/resolve", tok, map[string]string{"status": "resolved", "note": "banned 7 days"}), 204)
	e.expect(e.do("POST", "/api/admin/reports/"+r.ID+"/resolve", tok, map[string]string{"status": "dismissed"}), 400) // already closed
	e.now(`SELECT status FROM reports WHERE id = $1`, "resolved", r.ID)
	// a new report after it was closed opens a new one
	e.expect(report(map[string]any{"user_id": bob.ID, "reason": "spam"}), 201)
	e.now(`SELECT count(*)::text FROM reports`, "2")

	// closed reports are deleted after 180 days; open ones stay
	if _, err := e.db.Exec(`UPDATE reports SET resolved_at = NOW() - interval '181 days' WHERE id = $1`, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.now(`SELECT count(*)::text FROM reports`, "1")
	e.now(`SELECT count(*)::text FROM report_messages WHERE report_id = $1`, "0", r.ID)
}

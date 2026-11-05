//go:build integration

package integration

import (
	"net/url"
	"testing"
)

type msgSearchResult struct {
	Results []struct {
		MessageID string `json:"message_id"`
		Type      string `json:"type"`
		ChatID    string `json:"chat_id"`
		Content   string `json:"msg"`
	} `json:"results"`
	NextBefore   string `json:"next_before"`
	NextBeforeID string `json:"next_before_id"`
}

func (e *env) searchMsgs(u user, query url.Values, code int) msgSearchResult {
	e.t.Helper()
	var r msgSearchResult
	res := e.expect(e.do("GET", "/api/chats/search?"+query.Encode(), u.Token, nil), code)
	if code == 200 {
		res.json(e.t, &r)
	}
	return r
}

func hitTexts(r msgSearchResult) []string {
	out := []string{}
	for _, h := range r.Results {
		out = append(out, h.Content)
	}
	return out
}

func TestMessageSearch(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol, late := e.signup("alice"), e.signup("bobby"), e.signup("carol"), e.signup("later")
	e.befriend(alice, bob)
	e.befriend(bob, carol)
	e.send(bob, alice.ID, "private", "Dinner at the Thai place?")
	e.send(alice, bob.ID, "private", "ไปกินข้าวกันไหม")   // Thai: no spaces between words
	e.send(bob, carol.ID, "private", "dinner with carol") // not alice's chat
	gone := e.send(alice, bob.ID, "private", "dinner secret")
	e.expect(e.do("DELETE", "/api/chat/messages/"+gone, alice.Token, nil), 204)

	g := e.adminGroup("crew", alice, bob)
	e.send(bob, g, "public", "group DINNER plan")
	e.expect(e.join(late.Token, e.invite(alice.Token, g)), 201)
	e.send(bob, g, "public", "dinner moved to 8")

	r := e.searchMsgs(alice, url.Values{"q": {"dinner"}}, 200)
	if got := hitTexts(r); len(got) != 3 || got[0] != "dinner moved to 8" || got[2] != "Dinner at the Thai place?" {
		t.Fatalf("alice found %v", got)
	}
	// Thai matches inside a word
	if got := hitTexts(e.searchMsgs(alice, url.Values{"q": {"ข้าว"}}, 200)); len(got) != 1 {
		t.Fatalf("thai search %v", got)
	}
	// someone who joined later sees only what was sent since
	if got := hitTexts(e.searchMsgs(late, url.Values{"q": {"dinner"}}, 200)); len(got) != 1 || got[0] != "dinner moved to 8" {
		t.Fatalf("late joiner found %v", got)
	}
	// strangers find nothing of others' chats
	if got := hitTexts(e.searchMsgs(carol, url.Values{"q": {"thai"}}, 200)); len(got) != 0 {
		t.Fatalf("carol found %v", got)
	}

	// one chat, short query allowed there
	r = e.searchMsgs(alice, url.Values{"q": {"di"}, "type": {"private"}, "chat_id": {bob.ID}}, 200)
	if got := hitTexts(r); len(got) != 1 || r.Results[0].ChatID != bob.ID {
		t.Fatalf("scoped to bob: %v", got)
	}
	if got := hitTexts(e.searchMsgs(alice, url.Values{"q": {"dinner"}, "type": {"public"}, "chat_id": {g}}, 200)); len(got) != 2 {
		t.Fatalf("scoped to group: %v", got)
	}
	e.searchMsgs(alice, url.Values{"q": {"di"}}, 400)
	e.searchMsgs(alice, url.Values{"q": {"dinner"}, "before": {"yesterday"}}, 400)

	// paging: older than the last result, continuing exactly after it
	r = e.searchMsgs(alice, url.Values{"q": {"dinner"}, "limit": {"2"}}, 200)
	if len(r.Results) != 2 || r.NextBefore == "" {
		t.Fatalf("page 1 %+v", r)
	}
	r = e.searchMsgs(alice, url.Values{"q": {"dinner"}, "limit": {"2"}, "before": {r.NextBefore}, "before_id": {r.NextBeforeID}}, 200)
	if got := hitTexts(r); len(got) != 1 || got[0] != "Dinner at the Thai place?" {
		t.Fatalf("page 2 %v", got)
	}

	// big groups aren't indexed
	big := e.adminGroup("big", alice)
	if _, err := e.db.Exec(`UPDATE chat_groups SET current_member = 51, max_member = 100 WHERE id = $1`, big); err != nil {
		t.Fatal(err)
	}
	e.send(alice, big, "public", "dinner in a big group")
	if got := hitTexts(e.searchMsgs(alice, url.Values{"q": {"big group"}}, 200)); len(got) != 0 {
		t.Fatalf("big group indexed: %v", got)
	}
}

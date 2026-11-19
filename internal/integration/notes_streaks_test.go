//go:build integration

package integration

import (
	"strings"
	"testing"
)

type noteList struct {
	Notes []struct {
		UserID string `json:"user_id"`
		Text   string `json:"text"`
	} `json:"notes"`
}

func (e *env) notes(u user) noteList {
	e.t.Helper()
	var n noteList
	e.expect(e.do("GET", "/api/notes", u.Token, nil), 200).json(e.t, &n)
	return n
}

func TestNotes(t *testing.T) {
	e := newEnv(t)
	alice, bob, stranger := e.signup("alice"), e.signup("bobby"), e.signup("stranger")
	e.befriend(alice, bob)

	e.expect(e.do("PUT", "/api/users/me/note", alice.Token, map[string]string{"text": strings.Repeat("a", 61)}), 400)
	e.expect(e.do("PUT", "/api/users/me/note", alice.Token, map[string]string{"text": "two\nlines"}), 400)
	e.expect(e.do("PUT", "/api/users/me/note", alice.Token, map[string]string{"text": "   "}), 400)
	e.expect(e.do("PUT", "/api/users/me/note", bob.Token, map[string]string{"text": "first"}), 200)
	e.expect(e.do("PUT", "/api/users/me/note", alice.Token, map[string]string{"text": " 🎂 today! "}), 200)

	// mine first, then friends'; strangers see only their own (none)
	if n := e.notes(alice); len(n.Notes) != 2 || n.Notes[0].UserID != alice.ID || n.Notes[0].Text != "🎂 today!" || n.Notes[1].UserID != bob.ID {
		t.Fatalf("alice sees %+v", n)
	}
	if n := e.notes(stranger); len(n.Notes) != 0 {
		t.Fatalf("stranger sees %+v", n)
	}
	// a new note replaces the old one
	e.expect(e.do("PUT", "/api/users/me/note", bob.Token, map[string]string{"text": "second"}), 200)
	if n := e.notes(alice); len(n.Notes) != 2 || n.Notes[1].Text != "second" {
		t.Fatalf("after replace %+v", n)
	}
	// expired notes disappear (and the janitor deletes them)
	if _, err := e.db.Exec(`UPDATE notes SET expires_at = now() AT TIME ZONE 'UTC' - interval '1 minute' WHERE user_id = $1`, bob.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.notes(alice); len(n.Notes) != 1 {
		t.Fatalf("expired note still shown %+v", n)
	}
	if err := e.app.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.now(`SELECT count(*)::text FROM notes WHERE user_id = $1`, "0", bob.ID)

	// a block ends the friendship: notes are no longer shared
	e.expect(e.do("PUT", "/api/users/me/note", bob.Token, map[string]string{"text": "back"}), 200)
	e.expect(e.do("POST", "/api/users/blocks", alice.Token, map[string]string{"user_id": bob.ID}), 204)
	if n := e.notes(bob); len(n.Notes) != 1 || n.Notes[0].UserID != bob.ID {
		t.Fatalf("bob sees %+v after the block", n)
	}
	e.expect(e.do("DELETE", "/api/users/me/note", alice.Token, nil), 204)
	if n := e.notes(alice); len(n.Notes) != 0 {
		t.Fatalf("after delete %+v", n)
	}
}

type streakList struct {
	Conversations []struct {
		ID       string `json:"id"`
		Streak   int    `json:"streak"`
		EndsSoon bool   `json:"streak_ends_soon"`
	} `json:"conversations"`
}

func (e *env) streak(u user, with string) (int, bool) {
	e.t.Helper()
	var c streakList
	e.expect(e.do("GET", "/api/chats", u.Token, nil), 200).json(e.t, &c)
	for _, x := range c.Conversations {
		if x.ID == with {
			return x.Streak, x.EndsSoon
		}
	}
	e.t.Fatalf("no chat with %s", with)
	return 0, false
}

func TestStreaks(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	back := func(days int) { // as if everything so far happened days earlier
		t.Helper()
		if _, err := e.db.Exec(`UPDATE streaks SET lo_day = lo_day - $1::int, hi_day = hi_day - $1::int, done_day = done_day - $1::int`, days); err != nil {
			t.Fatal(err)
		}
	}

	e.send(alice, bob.ID, "private", "hi")
	if n, _ := e.streak(alice, bob.ID); n != 0 {
		t.Fatalf("one side only: %d", n)
	}
	e.send(bob, alice.ID, "private", "hey")
	e.send(bob, alice.ID, "private", "again") // once a day counts once
	if n, soon := e.streak(alice, bob.ID); n != 1 || soon {
		t.Fatalf("day 1: %d %v", n, soon)
	}
	back(1)
	if n, soon := e.streak(bob, alice.ID); n != 1 || !soon {
		t.Fatalf("next day, nothing yet: %d %v", n, soon)
	}
	e.send(bob, alice.ID, "private", "morning")
	if n, _ := e.streak(bob, alice.ID); n != 1 {
		t.Fatalf("only bob today: %d", n)
	}
	e.send(alice, bob.ID, "private", "morning!")
	if n, soon := e.streak(bob, alice.ID); n != 2 || soon {
		t.Fatalf("day 2: %d %v", n, soon)
	}

	// either of us hiding streaks hides it for both
	var p struct {
		HideLastSeen bool `json:"hide_last_seen"`
		HideStreaks  bool `json:"hide_streaks"`
	}
	e.expect(e.do("PUT", "/api/users/me/privacy", bob.Token, map[string]any{"hide_streaks": true}), 200).json(t, &p)
	if !p.HideStreaks || p.HideLastSeen {
		t.Fatalf("privacy %+v", p)
	}
	if n, _ := e.streak(alice, bob.ID); n != 0 {
		t.Fatalf("hidden by bob, alice sees %d", n)
	}
	e.expect(e.do("PUT", "/api/users/me/privacy", bob.Token, map[string]any{}), 400)
	e.expect(e.do("PUT", "/api/users/me/privacy", bob.Token, map[string]any{"hide_streaks": false}), 200)

	// a missed day ends it; the next day both write starts again at 1
	back(2)
	if n, _ := e.streak(alice, bob.ID); n != 0 {
		t.Fatalf("broken streak still shown: %d", n)
	}
	e.send(alice, bob.ID, "private", "sorry")
	e.send(bob, alice.ID, "private", "np")
	if n, _ := e.streak(alice, bob.ID); n != 1 {
		t.Fatalf("restarted: %d", n)
	}
}

func TestGIFUpload(t *testing.T) {
	e := newEnv(t)
	alice := e.signup("alice")
	e.expect(e.do("POST", "/api/media", alice.Token, map[string]any{"kind": "image", "content_type": "image/gif", "size": 1 << 20}), 201)
	e.expect(e.do("POST", "/api/media", alice.Token, map[string]any{"kind": "image", "content_type": "image/gif", "size": 6 << 20}), 400)
}

//go:build integration

package integration

import (
	"testing"
)

type storyFeed struct {
	Mine []struct {
		ID    string `json:"id"`
		Views int    `json:"views"`
	} `json:"mine"`
	Friends []struct {
		UserID  string `json:"user_id"`
		AllSeen bool   `json:"all_seen"`
		Stories []struct {
			ID    string `json:"id"`
			Seen  bool   `json:"seen"`
			Media *struct {
				ID string `json:"id"`
			} `json:"media"`
		} `json:"stories"`
	} `json:"friends"`
}

func (e *env) stories(u user) storyFeed {
	e.t.Helper()
	var f storyFeed
	e.expect(e.do("GET", "/api/stories", u.Token, nil), 200).json(e.t, &f)
	return f
}

func (e *env) postStory(u user) (story, photo string) {
	e.t.Helper()
	photo = e.uploadFile(u, "image", "image/jpeg", []byte("a story photo"))
	var s struct {
		ID string `json:"id"`
	}
	e.expect(e.do("POST", "/api/stories", u.Token, map[string]string{"media_id": photo}), 201).json(e.t, &s)
	return s.ID, photo
}

func TestStories(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol, stranger := e.signup("alice"), e.signup("bobby"), e.signup("carol"), e.signup("stranger")
	e.befriend(alice, bob)
	e.befriend(alice, carol)

	story, photo := e.postStory(alice)
	// a photo can back one story only; it must be yours
	e.expect(e.do("POST", "/api/stories", alice.Token, map[string]string{"media_id": photo}), 400)
	e.expect(e.do("POST", "/api/stories", bob.Token, map[string]string{"media_id": e.uploadFile(alice, "image", "image/jpeg", []byte("x"))}), 400)
	e.expect(e.do("POST", "/api/stories", alice.Token, map[string]string{}), 400)

	// friends see it, strangers don't (not the photo either)
	f := e.stories(bob)
	if len(f.Friends) != 1 || f.Friends[0].UserID != alice.ID || f.Friends[0].AllSeen || f.Friends[0].Stories[0].Media == nil || f.Friends[0].Stories[0].Media.ID != photo {
		t.Fatalf("bob sees %+v", f)
	}
	if f := e.stories(stranger); len(f.Friends) != 0 {
		t.Fatalf("stranger sees %+v", f)
	}
	e.expect(e.do("GET", "/api/media/"+photo, bob.Token, nil), 200)
	e.expect(e.do("GET", "/api/media/"+photo, stranger.Token, nil), 404)
	e.expect(e.do("POST", "/api/stories/"+story+"/view", stranger.Token, nil), 404)

	// views: counted for friends, not for yourself; only the poster sees who
	e.expect(e.do("POST", "/api/stories/"+story+"/view", bob.Token, nil), 204)
	e.expect(e.do("POST", "/api/stories/"+story+"/view", bob.Token, nil), 204) // once
	e.expect(e.do("POST", "/api/stories/"+story+"/view", alice.Token, nil), 204)
	if f := e.stories(bob); !f.Friends[0].AllSeen || !f.Friends[0].Stories[0].Seen {
		t.Fatalf("after viewing %+v", f)
	}
	if f := e.stories(alice); len(f.Mine) != 1 || f.Mine[0].Views != 1 {
		t.Fatalf("alice's own %+v", f)
	}
	var v struct {
		Viewers []struct {
			UserID string `json:"user_id"`
		} `json:"viewers"`
	}
	e.expect(e.do("GET", "/api/stories/"+story+"/viewers", alice.Token, nil), 200).json(t, &v)
	if len(v.Viewers) != 1 || v.Viewers[0].UserID != bob.ID {
		t.Fatalf("viewers %+v", v)
	}
	e.expect(e.do("GET", "/api/stories/"+story+"/viewers", bob.Token, nil), 404)

	// a reply is a private message to the poster
	e.expect(e.do("POST", "/api/chats/message", bob.Token, map[string]string{"msg": "nice!", "to_id": alice.ID, "type": "private", "story_id": story}), 200)
	e.expect(e.do("POST", "/api/chats/message", carol.Token, map[string]string{"msg": "x", "to_id": bob.ID, "type": "private", "story_id": story}), 400) // not bob's story (and not friends)
	e.befriend(bob, carol)
	e.expect(e.do("POST", "/api/chats/message", carol.Token, map[string]string{"msg": "x", "to_id": bob.ID, "type": "private", "story_id": story}), 400) // not bob's story
	e.expect(e.do("POST", "/api/chats/message", bob.Token, map[string]string{"msg": "x", "to_id": alice.ID, "type": "private", "story_id": "nope"}), 400)
	var h struct {
		MsgList []struct {
			Info struct {
				Msg struct {
					StoryID string `json:"story_id"`
				}
			} `json:"message_info"`
		} `json:"msgList"`
	}
	e.expect(e.do("GET", "/api/chat/"+bob.ID+"/messages", alice.Token, nil), 200).json(t, &h)
	if len(h.MsgList) != 1 || h.MsgList[0].Info.Msg.StoryID != story {
		t.Fatalf("reply in history %+v", h)
	}

	// muting someone's stories hides them; unmuting brings them back
	e.expect(e.do("PUT", "/api/stories/mutes/"+alice.ID, carol.Token, nil), 204)
	if f := e.stories(carol); len(f.Friends) != 0 {
		t.Fatalf("muted, carol sees %+v", f)
	}
	e.expect(e.do("PUT", "/api/stories/mutes/"+carol.ID, carol.Token, nil), 400)
	e.expect(e.do("DELETE", "/api/stories/mutes/"+alice.ID, carol.Token, nil), 204)
	if f := e.stories(carol); len(f.Friends) != 1 {
		t.Fatalf("unmuted, carol sees %+v", f)
	}

	// a block ends the friendship: the story and its photo are gone for them
	e.expect(e.do("POST", "/api/users/blocks", bob.Token, map[string]string{"user_id": alice.ID}), 204)
	if f := e.stories(bob); len(f.Friends) != 0 {
		t.Fatalf("after the block bob sees %+v", f)
	}
	e.expect(e.do("GET", "/api/media/"+photo, bob.Token, nil), 404)

	// deleting: only the poster; then nobody sees it or its photo
	e.expect(e.do("DELETE", "/api/stories/"+story, carol.Token, nil), 404)
	e.expect(e.do("DELETE", "/api/stories/"+story, alice.Token, nil), 204)
	if f := e.stories(carol); len(f.Friends) != 0 {
		t.Fatalf("deleted story still listed %+v", f)
	}
	e.expect(e.do("GET", "/api/media/"+photo, carol.Token, nil), 404)

	// after 24 hours it's gone, and the cleanup removes it and its photo
	old, oldPhoto := e.postStory(alice)
	if _, err := e.db.Exec(`UPDATE stories SET expires_at = now() AT TIME ZONE 'UTC' - interval '1 minute' WHERE id = $1`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE media SET expires_at = now() AT TIME ZONE 'UTC' - interval '1 minute' WHERE id = $1`, oldPhoto); err != nil {
		t.Fatal(err)
	}
	if f := e.stories(carol); len(f.Friends) != 0 {
		t.Fatalf("expired story listed %+v", f)
	}
	if err := e.app.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.now(`SELECT count(*)::text FROM stories WHERE id = $1`, "0", old)
	e.now(`SELECT (deleted_at IS NOT NULL)::text FROM media WHERE id = $1`, "true", oldPhoto)

	// at most 30 live stories
	if _, err := e.db.Exec(`INSERT INTO stories (id, user_id, created_at, expires_at)
		SELECT gen_random_uuid(), $1, now() AT TIME ZONE 'UTC', now() AT TIME ZONE 'UTC' + interval '1 hour' FROM generate_series(1, 30)`, carol.ID); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("POST", "/api/stories", carol.Token, map[string]string{"media_id": e.uploadFile(carol, "image", "image/jpeg", []byte("y"))}), 429)
}

// Pigeon stickers are stored with the story and come back to friends.
func TestStoryStickers(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.signup("alice"), e.signup("bobby")
	e.befriend(alice, bob)
	post := func(stickers any, want int) {
		t.Helper()
		body := map[string]any{"media_id": e.uploadFile(alice, "image", "image/jpeg", []byte("photo")), "stickers": stickers}
		e.expect(e.do("POST", "/api/stories", alice.Token, body), want)
	}
	post([]map[string]any{{"type": "pigeon", "anim": "wave_01", "x": 0.5, "y": 0.62, "size": 0.42, "rotation": -8}}, 201)
	post([]map[string]any{{"type": "pigeon", "anim": "highfive_ask_01", "x": 0.5, "y": 0.5, "size": 0.3}}, 400)
	post([]map[string]any{{"type": "pigeon", "anim": "wave_01", "x": 2, "y": 0.5, "size": 0.3}}, 400)
	post("not a list", 400)
	post([]any{}, 201) // none: like before

	var feed struct {
		Mine []struct {
			Stickers []map[string]any `json:"stickers"`
		} `json:"mine"`
		Friends []struct {
			Stories []struct {
				Stickers []struct {
					Type string  `json:"type"`
					Anim string  `json:"anim"`
					Y    float64 `json:"y"`
				} `json:"stickers"`
			} `json:"stories"`
		} `json:"friends"`
	}
	e.expect(e.do("GET", "/api/stories", alice.Token, nil), 200).json(t, &feed)
	if len(feed.Mine) != 2 || len(feed.Mine[0].Stickers) != 1 || feed.Mine[1].Stickers != nil {
		t.Fatalf("mine %+v", feed.Mine)
	}
	e.expect(e.do("GET", "/api/stories", bob.Token, nil), 200).json(t, &feed)
	st := feed.Friends[0].Stories
	if len(st) != 2 || len(st[0].Stickers) != 1 || st[0].Stickers[0].Anim != "wave_01" || st[0].Stickers[0].Y != 0.62 || st[1].Stickers != nil {
		t.Fatalf("bob sees %+v", st)
	}
}

// Mentioning friends in a story, and the mentioned sharing it to their own.
func TestStoryMentionsAndReposts(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol, dave := e.signup("alice"), e.signup("bobby"), e.signup("carol"), e.signup("david")
	e.befriend(alice, bob)
	e.befriend(alice, carol)
	e.befriend(bob, dave) // dave isn't alice's friend
	e.device(carol, "tok-carol")
	e.device(alice, "tok-alice")
	bobWS := e.dial(bob.Token)

	mention := func(u user) map[string]any {
		return map[string]any{"type": "mention", "user_id": u.ID, "x": 0.4, "y": 0.2, "size": 0.3, "rotation": 0}
	}
	post := func(stickers []map[string]any, want int) string {
		t.Helper()
		var s struct {
			ID string `json:"id"`
		}
		r := e.expect(e.do("POST", "/api/stories", alice.Token, map[string]any{"media_id": e.uploadFile(alice, "image", "image/jpeg", []byte("beach")), "stickers": stickers}), want)
		if want == 201 {
			r.json(t, &s)
		}
		return s.ID
	}
	post([]map[string]any{mention(dave)}, 400) // not her friend
	story := post([]map[string]any{mention(bob), mention(carol), {"type": "pigeon", "anim": "wave_01", "x": 0.5, "y": 0.7, "size": 0.4, "rotation": 0}}, 201)

	// bob (online) gets the live event, carol (offline) the push
	if _, p := nextOf(t, bobWS, "StoryMention"); p["story_id"] != story || p["from_id"] != alice.ID {
		t.Fatalf("event %v", p)
	}
	if m := e.waitPush("tok-carol", 1)[0]; m.Body != "mentioned you in their story" || m.Title != "alice" || m.Data["story_id"] != story {
		t.Fatalf("push %+v", m)
	}
	// viewers see who was mentioned, by name
	var feed struct {
		Friends []struct {
			UserID  string `json:"user_id"`
			Stories []struct {
				ID       string `json:"id"`
				Stickers []struct {
					Type   string `json:"type"`
					UserID string `json:"user_id"`
					Name   string `json:"name"`
				} `json:"stickers"`
				RepostOf *struct {
					StoryID string `json:"story_id"`
					UserID  string `json:"user_id"`
					Name    string `json:"name"`
				} `json:"repost_of"`
			} `json:"stories"`
		} `json:"friends"`
	}
	e.expect(e.do("GET", "/api/stories", carol.Token, nil), 200).json(t, &feed)
	if st := feed.Friends[0].Stories[0]; len(st.Stickers) != 3 || st.Stickers[0].Name != "bobby" || st.Stickers[1].Name != "carol" || st.Stickers[2].Name != "" {
		t.Fatalf("stickers %+v", st.Stickers)
	}

	// only someone mentioned can repost, once
	e.expect(e.do("POST", "/api/stories/"+story+"/repost", dave.Token, nil), 404)
	var rp struct {
		ID       string `json:"id"`
		RepostOf struct {
			Name string `json:"name"`
		} `json:"repost_of"`
		Media struct {
			ID string `json:"id"`
		} `json:"media"`
	}
	e.expect(e.do("POST", "/api/stories/"+story+"/repost", bob.Token, nil), 201).json(t, &rp)
	if rp.RepostOf.Name != "alice" || rp.Media.ID == "" {
		t.Fatalf("repost %+v", rp)
	}
	e.expect(e.do("POST", "/api/stories/"+story+"/repost", bob.Token, nil), 409)
	e.expect(e.do("POST", "/api/stories/"+rp.ID+"/repost", carol.Token, nil), 404) // not a repost of a repost
	if m := e.waitPush("tok-alice", 1)[0]; m.Body != "shared your story to their story" || m.Title != "bobby" {
		t.Fatalf("repost push %+v", m)
	}
	// bob's friends (dave, not alice's) see it "from alice", with its own photo
	feed.Friends = nil
	e.expect(e.do("GET", "/api/stories", dave.Token, nil), 200).json(t, &feed)
	if len(feed.Friends) != 1 || feed.Friends[0].UserID != bob.ID || feed.Friends[0].Stories[0].RepostOf == nil || feed.Friends[0].Stories[0].RepostOf.UserID != alice.ID {
		t.Fatalf("dave sees %+v", feed.Friends)
	}
	e.expect(e.do("GET", "/api/media/"+rp.Media.ID, dave.Token, nil), 200)

	// alice deletes hers: the repost goes too
	e.expect(e.do("DELETE", "/api/stories/"+story, alice.Token, nil), 204)
	feed.Friends = nil
	e.expect(e.do("GET", "/api/stories", dave.Token, nil), 200).json(t, &feed)
	if len(feed.Friends) != 0 {
		t.Fatalf("repost outlived the original: %+v", feed.Friends)
	}
	e.expect(e.do("GET", "/api/media/"+rp.Media.ID, dave.Token, nil), 404)
}

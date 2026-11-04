//go:build integration

package integration

import (
	"testing"
)

type suggestions struct {
	List []struct {
		ID              string   `json:"id"`
		Name            string   `json:"name"`
		SharedInterests []string `json:"shared_interests"`
		MutualFriends   int      `json:"mutual_friends"`
		Location        *struct {
			Name string `json:"name"`
		} `json:"location"`
	} `json:"suggestions"`
}

func (e *env) profile(u user, body map[string]any) {
	e.t.Helper()
	e.expect(e.do("PATCH", "/api/users/me", u.Token, body), 200)
}

func TestDiscoverRanking(t *testing.T) {
	e := newEnv(t)
	me := e.seedUser("me")
	friend := e.seedUser("friend")
	mutual := e.seedUser("mutual")       // friend of my friend
	local := e.seedUser("local")         // same city + shared interest
	country := e.seedUser("country")     // same country only
	hidden := e.seedUser("hidden")       // matches but not discoverable
	faraway := e.seedUser("faraway")     // discoverable, nothing in common
	blocked := e.seedUser("blocked")     // matches but blocked
	requested := e.seedUser("requested") // matches but a request is pending

	yes := map[string]any{"discoverable": true}
	e.expect(e.do("GET", "/api/discover", me.Token, nil), 403) // not visible yourself yet

	e.profile(me, map[string]any{"discoverable": true, "location": "th-bangkok", "interests": []string{"gaming", "kpop"}})
	e.befriend(me, friend)
	e.befriend(friend, mutual)
	e.profile(mutual, yes)
	e.profile(local, map[string]any{"discoverable": true, "location": "th-bangkok", "interests": []string{"gaming", "anime"}})
	e.profile(country, map[string]any{"discoverable": true, "location": "th-chiang-mai"})
	e.profile(hidden, map[string]any{"location": "th-bangkok", "interests": []string{"gaming"}})
	e.profile(faraway, map[string]any{"discoverable": true, "location": "europe", "interests": []string{"history"}})
	e.profile(blocked, map[string]any{"discoverable": true, "location": "th-bangkok"})
	e.expect(e.do("POST", "/api/users/blocks", blocked.Token, map[string]string{"user_id": me.ID}), 204)
	e.profile(requested, map[string]any{"discoverable": true, "location": "th-bangkok"})
	e.expect(e.do("POST", "/api/friends/requests", requested.Token, map[string]string{"to_id": me.ID}), 200)

	var s suggestions
	e.expect(e.do("GET", "/api/discover", me.Token, nil), 200).json(t, &s)
	var names []string
	for _, x := range s.List {
		names = append(names, x.Name)
	}
	want := []string{"local", "mutual", "country"}
	if len(names) != len(want) {
		t.Fatalf("suggestions %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("suggestions %v, want %v", names, want)
		}
	}
	if l := s.List[0]; len(l.SharedInterests) != 1 || l.SharedInterests[0] != "gaming" || l.Location == nil || l.Location.Name != "Bangkok" {
		t.Fatalf("local %+v", l)
	}
	if s.List[1].MutualFriends != 1 {
		t.Fatalf("mutual %+v", s.List[1])
	}
	e.expect(e.do("GET", "/api/discover?limit=0", me.Token, nil), 400)
	e.expect(e.do("GET", "/api/discover?page=10", me.Token, nil), 400)
}

func TestDiscoveryProfileFields(t *testing.T) {
	e := newEnv(t)
	alice, friend, stranger := e.seedUser("alice"), e.seedUser("friend"), e.seedUser("stranger")
	e.befriend(alice, friend)

	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]any{"location": "atlantis"}), 400)
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]any{"interests": []string{"not-a-hobby"}}), 400)
	var me struct {
		Location *struct {
			ID      string `json:"id"`
			Country string `json:"country"`
		} `json:"location"`
		Interests    []string `json:"interests"`
		Discoverable bool     `json:"discoverable"`
	}
	e.expect(e.do("PATCH", "/api/users/me", alice.Token, map[string]any{"location": "mm-yangon", "interests": []string{"music", "coding"}}), 200).json(t, &me)
	if me.Location == nil || me.Location.Country != "mm" || len(me.Interests) != 2 || me.Discoverable {
		t.Fatalf("me %+v", me)
	}

	// not discoverable: friends see location and interests, strangers don't
	var p struct {
		Location  *struct{} `json:"location"`
		Interests []string  `json:"interests"`
	}
	e.expect(e.do("GET", "/api/users/"+alice.ID, friend.Token, nil), 200).json(t, &p)
	if p.Location == nil || len(p.Interests) != 2 {
		t.Fatalf("friend sees %+v", p)
	}
	p.Location, p.Interests = nil, nil
	e.expect(e.do("GET", "/api/users/"+alice.ID, stranger.Token, nil), 200).json(t, &p)
	if p.Location != nil || len(p.Interests) != 0 {
		t.Fatalf("stranger sees %+v", p)
	}

	var opts struct {
		Countries []struct {
			Code string `json:"code"`
		} `json:"countries"`
		Interests    []struct{} `json:"interests"`
		MaxInterests int        `json:"max_interests"`
	}
	e.expect(e.do("GET", "/api/discover/options", alice.Token, nil), 200).json(t, &opts)
	if len(opts.Countries) < 10 || opts.Countries[0].Code != "th" || len(opts.Interests) < 40 || opts.MaxInterests != 10 {
		t.Fatalf("options %d countries, %d interests", len(opts.Countries), len(opts.Interests))
	}
}

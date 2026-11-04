package presence

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/friends"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeDB struct {
	mu   sync.Mutex
	hide map[uuid.UUID]bool
	seen map[uuid.UUID]time.Time
}

func (f *fakeDB) PresenceOf(_ context.Context, ids []uuid.UUID) ([]database.PresenceOfRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []database.PresenceOfRow
	for _, id := range ids {
		t, ok := f.seen[id]
		out = append(out, database.PresenceOfRow{ID: id, HideLastSeen: f.hide[id], LastSeenAt: pgtype.Timestamp{Time: t, Valid: ok}})
	}
	return out, nil
}
func (f *fakeDB) SetHideLastSeen(_ context.Context, a database.SetHideLastSeenParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hide[a.ID] = a.Hide
	return nil
}
func (f *fakeDB) TouchLastSeen(_ context.Context, a database.TouchLastSeenParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen[a.ID] = a.Now.Time
	return nil
}

type fakeFriends map[uuid.UUID][]uuid.UUID

func (f fakeFriends) Relationships(_ context.Context, me uuid.UUID) (friends.Snapshot, error) {
	var s friends.Snapshot
	for _, id := range f[me] {
		s.Friends = append(s.Friends, friends.FriendMetaData{UserID: id})
	}
	return s, nil
}

type sent struct {
	to []uuid.UUID
	ev ws.OutPresenceEvent
}

type fakeHub struct {
	mu     sync.Mutex
	online map[uuid.UUID]bool
	out    chan sent
}

func (h *fakeHub) Online(id uuid.UUID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.online[id]
}
func (h *fakeHub) set(id uuid.UUID, on bool) {
	h.mu.Lock()
	h.online[id] = on
	h.mu.Unlock()
}
func (h *fakeHub) WriteToUsers(to []uuid.UUID, ev ws.Event) error {
	h.out <- sent{to: to, ev: ev.Payload.(ws.OutPresenceEvent)}
	return nil
}

type fixture struct {
	svc                  *Service
	db                   *fakeDB
	hub                  *fakeHub
	alice, bob, carol, x uuid.UUID
}

// alice is friends with bob and carol; x is a stranger
func newFixture() *fixture {
	f := &fixture{alice: uuid.New(), bob: uuid.New(), carol: uuid.New(), x: uuid.New()}
	f.db = &fakeDB{hide: map[uuid.UUID]bool{}, seen: map[uuid.UUID]time.Time{}}
	f.hub = &fakeHub{online: map[uuid.UUID]bool{}, out: make(chan sent, 10)}
	fr := fakeFriends{f.alice: {f.bob, f.carol}, f.bob: {f.alice}, f.carol: {f.alice}}
	f.svc = NewService(f.db, fr, f.hub, slog.New(slog.DiscardHandler))
	f.svc.grace = 30 * time.Millisecond
	return f
}

func (f *fixture) next(t *testing.T) sent {
	t.Helper()
	select {
	case s := <-f.hub.out:
		return s
	case <-time.After(time.Second):
		t.Fatal("no presence event")
	}
	return sent{}
}

func (f *fixture) none(t *testing.T, wait time.Duration) {
	t.Helper()
	select {
	case s := <-f.hub.out:
		t.Fatalf("unexpected event %+v", s)
	case <-time.After(wait):
	}
}

func TestOnlineGoesOnlyToOnlineFriends(t *testing.T) {
	f := newFixture()
	f.hub.set(f.bob, true)
	f.hub.set(f.x, true)
	f.hub.set(f.alice, true)
	f.svc.Changed(f.alice, true)
	s := f.next(t)
	if len(s.to) != 1 || s.to[0] != f.bob || !s.ev.Online || s.ev.Hidden || s.ev.LastSeenAt != nil {
		t.Fatalf("got %+v", s)
	}
}

func TestOfflineWaitsOutTheGrace(t *testing.T) {
	f := newFixture()
	f.hub.set(f.bob, true)

	// a quick reconnect is never announced
	f.svc.Changed(f.alice, false)
	f.hub.set(f.alice, true)
	f.svc.Changed(f.alice, true)
	f.none(t, 80*time.Millisecond)

	// a real disconnect is, with last seen
	f.hub.set(f.alice, false)
	f.svc.Changed(f.alice, false)
	s := f.next(t)
	if s.ev.Online || s.ev.LastSeenAt == nil {
		t.Fatalf("got %+v", s.ev)
	}
}

func TestHiddenIsReciprocal(t *testing.T) {
	f := newFixture()
	f.hub.set(f.bob, true)
	f.hub.set(f.carol, true)
	f.hub.set(f.alice, true)
	ctx := context.Background()

	// carol hides hers: she gets no news of alice, and sees all hidden
	if err := f.svc.SetHidden(ctx, f.carol, true); err != nil {
		t.Fatal(err)
	}
	if s := f.next(t); !s.ev.Hidden || s.ev.Online || len(s.to) != 1 || s.to[0] != f.alice {
		t.Fatalf("carol hiding: %+v", s)
	}
	f.svc.Changed(f.alice, true)
	if s := f.next(t); len(s.to) != 1 || s.to[0] != f.bob {
		t.Fatalf("alice online went to %v, want only bob", s.to)
	}
	hidden, list, err := f.svc.Friends(ctx, f.carol)
	if err != nil || !hidden || len(list) != 1 || !list[0].Hidden || list[0].Online {
		t.Fatalf("carol's view: %v %+v %v", hidden, list, err)
	}

	// alice sees carol as hidden, bob as online
	_, list, _ = f.svc.Friends(ctx, f.alice)
	for _, e := range list {
		switch e.UserID {
		case f.bob:
			if e.Hidden || !e.Online {
				t.Fatalf("bob: %+v", e)
			}
		case f.carol:
			if !e.Hidden || e.Online || e.LastSeenAt != nil {
				t.Fatalf("carol: %+v", e)
			}
		}
	}
}

func TestStrangersSeeNothing(t *testing.T) {
	f := newFixture()
	_, list, _ := f.svc.Friends(context.Background(), f.x)
	if len(list) != 0 {
		t.Fatalf("stranger sees %+v", list)
	}
}

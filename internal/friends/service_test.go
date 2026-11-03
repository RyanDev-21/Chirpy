package friends

import (
	"context"
	"errors"
	"sync"
	"testing"

	"RyanDev-21.com/Chirpy/internal/testutil"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
)

type row struct {
	from, to uuid.UUID
	status   string
}

// fakeRepo enforces the same rules as postgres: one row per pair.
type fakeRepo struct {
	mu    sync.Mutex
	rows  map[uuid.UUID]*row
	names map[uuid.UUID]string
	loads int
}

func (f *fakeRepo) pair(a, b uuid.UUID) *row {
	for _, r := range f.rows {
		if (r.from == a && r.to == b) || (r.from == b && r.to == a) {
			return r
		}
	}
	return nil
}

func (f *fakeRepo) Snapshot(_ context.Context, me uuid.UUID) (Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	s := Snapshot{Friends: []FriendMetaData{}, Incoming: map[uuid.UUID]FriendMetaData{}, Outgoing: map[uuid.UUID]FriendMetaData{}}
	for id, r := range f.rows {
		switch {
		case r.status == "confirm" && r.from == me:
			s.Friends = append(s.Friends, FriendMetaData{UserID: r.to, Name: f.names[r.to]})
		case r.status == "confirm" && r.to == me:
			s.Friends = append(s.Friends, FriendMetaData{UserID: r.from, Name: f.names[r.from]})
		case r.to == me:
			s.Incoming[id] = FriendMetaData{UserID: r.from, Name: f.names[r.from]}
		case r.from == me:
			s.Outgoing[id] = FriendMetaData{UserID: r.to, Name: f.names[r.to]}
		}
	}
	return s, nil
}

func (f *fakeRepo) Create(_ context.Context, id, from, to uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.pair(from, to); r != nil {
		if r.status == "confirm" {
			return ErrAlreadyFriends
		}
		return ErrRequestExists
	}
	f.rows[id] = &row{from: from, to: to, status: "pending"}
	return nil
}

func (f *fakeRepo) resolve(id, me uuid.UUID, receiver bool, del bool) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok || r.status != "pending" || (receiver && r.to != me) || (!receiver && r.from != me) {
		return uuid.Nil, ErrNotFound
	}
	other := r.from
	if !receiver {
		other = r.to
	}
	if del {
		delete(f.rows, id)
	} else {
		r.status = "confirm"
	}
	return other, nil
}

func (f *fakeRepo) Accept(_ context.Context, id, me uuid.UUID) (uuid.UUID, error) {
	return f.resolve(id, me, true, false)
}
func (f *fakeRepo) Deny(_ context.Context, id, me uuid.UUID) (uuid.UUID, error) {
	return f.resolve(id, me, true, true)
}
func (f *fakeRepo) Cancel(_ context.Context, id, me uuid.UUID) (uuid.UUID, error) {
	return f.resolve(id, me, false, true)
}

type fakeNames map[uuid.UUID]string

func (n fakeNames) GetName(_ context.Context, id uuid.UUID) (string, error) {
	if name, ok := n[id]; ok {
		return name, nil
	}
	return "", users.ErrUserNotFound
}

type pushed struct {
	to uuid.UUID
	ev ws.Event
}

type fakeHub struct {
	mu   sync.Mutex
	sent []pushed
}

func (h *fakeHub) WriteIntoConnection(to uuid.UUID, ev ws.Event) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sent = append(h.sent, pushed{to, ev})
	return nil
}

type fixture struct {
	svc        Service
	store      Store
	repo       *fakeRepo
	hub        *fakeHub
	alice, bob uuid.UUID
}

func setup(t *testing.T) fixture {
	t.Helper()
	rdb, _ := testutil.Redis(t)
	alice, bob := uuid.New(), uuid.New()
	names := fakeNames{alice: "alice", bob: "bob"}
	repo := &fakeRepo{rows: map[uuid.UUID]*row{}, names: names}
	f := fixture{store: NewStore(rdb), repo: repo, hub: &fakeHub{}, alice: alice, bob: bob}
	f.svc = NewService(repo, f.store, names, f.hub, testutil.Logger())
	return f
}

func TestSendAcceptFlow(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	// warm both caches so the test proves writes invalidate them
	_, _ = f.svc.Relationships(ctx, f.alice)
	_, _ = f.svc.Relationships(ctx, f.bob)

	reqID, err := f.svc.Send(ctx, f.alice, f.bob)
	if err != nil {
		t.Fatal(err)
	}
	bobRel, _ := f.svc.Relationships(ctx, f.bob)
	if got := bobRel.Incoming[reqID]; got.UserID != f.alice || got.Name != "alice" {
		t.Fatalf("bob incoming = %+v", bobRel.Incoming)
	}
	if err := f.svc.Accept(ctx, f.alice, reqID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sender accept err = %v", err)
	}
	if err := f.svc.Accept(ctx, f.bob, reqID); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]uuid.UUID{{f.alice, f.bob}, {f.bob, f.alice}} {
		if ok, err := f.svc.AreFriends(ctx, p[0], p[1]); err != nil || !ok {
			t.Fatalf("AreFriends(%v) = %v, %v", p, ok, err)
		}
	}
	if len(f.hub.sent) != 2 || f.hub.sent[0].to != f.bob || f.hub.sent[1].to != f.alice {
		t.Fatalf("pushes = %+v", f.hub.sent)
	}
}

func TestSendRejections(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.svc.Send(ctx, f.alice, f.alice); !errors.Is(err, ErrSelfRequest) {
		t.Fatalf("self: %v", err)
	}
	if _, err := f.svc.Send(ctx, f.alice, uuid.New()); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("unknown: %v", err)
	}
	reqID, _ := f.svc.Send(ctx, f.alice, f.bob)
	if _, err := f.svc.Send(ctx, f.bob, f.alice); !errors.Is(err, ErrRequestExists) {
		t.Fatalf("crossing request: %v", err)
	}
	_ = f.svc.Accept(ctx, f.bob, reqID)
	if _, err := f.svc.Send(ctx, f.bob, f.alice); !errors.Is(err, ErrAlreadyFriends) {
		t.Fatalf("already friends: %v", err)
	}
}

func TestDenyAndCancel(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	reqID, _ := f.svc.Send(ctx, f.alice, f.bob)
	if err := f.svc.Deny(ctx, f.bob, reqID); err != nil {
		t.Fatal(err)
	}
	reqID, err := f.svc.Send(ctx, f.alice, f.bob)
	if err != nil {
		t.Fatalf("resend after deny: %v", err)
	}
	if err := f.svc.Cancel(ctx, f.bob, reqID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("receiver cannot cancel: %v", err)
	}
	if err := f.svc.Cancel(ctx, f.alice, reqID); err != nil {
		t.Fatal(err)
	}
	if rel, _ := f.svc.Relationships(ctx, f.bob); len(rel.Incoming) != 0 {
		t.Fatalf("cancelled request still visible: %+v", rel.Incoming)
	}
}

func TestReadsAreCached(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for range 3 {
		_, _ = f.svc.AreFriends(ctx, f.alice, f.bob)
	}
	if f.repo.loads != 1 {
		t.Fatalf("postgres read %d times, want 1", f.repo.loads)
	}
}

func TestStaleReadIsNotCached(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	stale := Snapshot{Friends: []FriendMetaData{{UserID: f.bob, Name: "bob"}}}

	id := f.alice.String()
	v, _ := f.store.Version(ctx, id)
	_ = f.store.Invalidate(ctx, id) // a write lands while the reader is in postgres
	if err := f.store.Put(ctx, id, v, stale); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := f.store.Get(ctx, id); found {
		t.Fatal("a snapshot read before a write must not be cached")
	}

	v, _ = f.store.Version(ctx, id)
	_ = f.store.Put(ctx, id, v, stale)
	if _, found, _ := f.store.Get(ctx, id); !found {
		t.Fatal("an up-to-date snapshot should be cached")
	}
}

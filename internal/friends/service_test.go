package friends

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"RyanDev-21.com/Chirpy/internal/testutil"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
)

type fakeRepo struct {
	mu    sync.Mutex
	snaps map[uuid.UUID]Snapshot
	loads int
}

func (f *fakeRepo) Snapshot(_ context.Context, id uuid.UUID) (Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	if s, ok := f.snaps[id]; ok {
		return s, nil
	}
	return Snapshot{Incoming: map[uuid.UUID]FriendMetaData{}, Outgoing: map[uuid.UUID]FriendMetaData{}}, nil
}
func (f *fakeRepo) Create(context.Context, JobRequest) error { return nil }
func (f *fakeRepo) Accept(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	return 1, nil
}
func (f *fakeRepo) DeleteBySender(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	return 1, nil
}
func (f *fakeRepo) DeleteByReceiver(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	return 0, nil
}

type fakeNames map[uuid.UUID]string

func (n fakeNames) GetName(_ context.Context, id uuid.UUID) (string, error) {
	if name, ok := n[id]; ok {
		return name, nil
	}
	return "", users.NoUserFoundErr
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

type published struct {
	topic   string
	payload json.RawMessage
}

type fakeQueue struct {
	mu   sync.Mutex
	jobs []published
}

func (q *fakeQueue) Publish(_ context.Context, topic string, payload any) error {
	raw, _ := json.Marshal(payload)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, published{topic, raw})
	return nil
}

func (q *fakeQueue) topics() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []string
	for _, j := range q.jobs {
		out = append(out, j.topic)
	}
	return out
}

type fixture struct {
	svc        Service
	repo       *fakeRepo
	hub        *fakeHub
	queue      *fakeQueue
	alice, bob uuid.UUID
}

func setup(t *testing.T) fixture {
	t.Helper()
	rdb, _ := testutil.Redis(t)
	f := fixture{
		repo:  &fakeRepo{snaps: map[uuid.UUID]Snapshot{}},
		hub:   &fakeHub{},
		queue: &fakeQueue{},
		alice: uuid.New(),
		bob:   uuid.New(),
	}
	names := fakeNames{f.alice: "alice", f.bob: "bob"}
	f.svc = NewService(f.repo, NewStore(rdb), names, f.hub, f.queue, testutil.Logger())
	return f
}

func TestSendAcceptFlow(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	reqID, err := f.svc.Send(ctx, f.alice, f.bob)
	if err != nil {
		t.Fatal(err)
	}
	bobRel, _ := f.svc.Relationships(ctx, f.bob)
	if got := bobRel.Incoming[reqID]; got.UserID != f.alice || got.Name != "alice" {
		t.Fatalf("bob incoming = %+v", bobRel.Incoming)
	}
	aliceRel, _ := f.svc.Relationships(ctx, f.alice)
	if got := aliceRel.Outgoing[reqID]; got.UserID != f.bob {
		t.Fatalf("alice outgoing = %+v", aliceRel.Outgoing)
	}

	// only the receiver can accept
	if err := f.svc.Accept(ctx, f.alice, reqID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sender accept err = %v", err)
	}
	if err := f.svc.Accept(ctx, f.bob, reqID); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]uuid.UUID{{f.alice, f.bob}, {f.bob, f.alice}} {
		ok, err := f.svc.AreFriends(ctx, pair[0], pair[1])
		if err != nil || !ok {
			t.Fatalf("AreFriends(%v) = %v, %v", pair, ok, err)
		}
	}
	rel, _ := f.svc.Relationships(ctx, f.bob)
	if len(rel.Incoming) != 0 || len(rel.Friends) != 1 || rel.Friends[0].Name != "alice" {
		t.Fatalf("after accept bob = %+v", rel)
	}

	if got := f.queue.topics(); len(got) != 2 || got[0] != TopicCreate || got[1] != TopicAccept {
		t.Fatalf("published topics = %v", got)
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
	reqID, err := f.svc.Send(ctx, f.alice, f.bob)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Send(ctx, f.alice, f.bob); !errors.Is(err, ErrRequestExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := f.svc.Send(ctx, f.bob, f.alice); !errors.Is(err, ErrRequestExists) {
		t.Fatalf("crossing request: %v", err)
	}
	if err := f.svc.Accept(ctx, f.bob, reqID); err != nil {
		t.Fatal(err)
	}
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
	if ok, _ := f.svc.AreFriends(ctx, f.alice, f.bob); ok {
		t.Fatal("deny must not create a friendship")
	}
	// a denied request does not block a new one
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
	rel, _ := f.svc.Relationships(ctx, f.bob)
	if len(rel.Incoming) != 0 {
		t.Fatalf("cancelled request still visible: %+v", rel.Incoming)
	}
}

func TestLoadsFromPostgresOnce(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	carol := uuid.New()
	f.repo.snaps[f.alice] = Snapshot{
		Friends:  []FriendMetaData{{UserID: carol, Name: "carol"}},
		Incoming: map[uuid.UUID]FriendMetaData{},
		Outgoing: map[uuid.UUID]FriendMetaData{},
	}
	for range 3 {
		ok, err := f.svc.AreFriends(ctx, f.alice, carol)
		if err != nil || !ok {
			t.Fatalf("AreFriends = %v %v", ok, err)
		}
	}
	if f.repo.loads != 1 {
		t.Fatalf("snapshot loaded %d times, want 1", f.repo.loads)
	}
}

func TestWorkersRetryUntilPersisted(t *testing.T) {
	f := setup(t)
	// DeleteByReceiver reports 0 rows: the create job has not run yet
	if err := f.svc.HandleDeny(context.Background(), JobRequest{}); !errors.Is(err, errNotPersistedYet) {
		t.Fatalf("err = %v", err)
	}
}

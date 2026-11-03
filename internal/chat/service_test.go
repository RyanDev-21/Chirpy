package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/testutil"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
)

type fakeRepo struct {
	history []MessageMetaDataRes
	since   int
}

func (f *fakeRepo) AddPrivate(context.Context, MessageMetaDataRes) error { return nil }
func (f *fakeRepo) AddPublic(context.Context, MessageMetaDataRes) error  { return nil }
func (f *fakeRepo) RecentPrivate(_ context.Context, _, _ uuid.UUID, n int) ([]MessageMetaDataRes, error) {
	return f.history[max(0, len(f.history)-n):], nil
}
func (f *fakeRepo) PrivateSince(context.Context, uuid.UUID, uuid.UUID, time.Time, int) ([]MessageMetaDataRes, error) {
	f.since++
	return nil, nil
}
func (f *fakeRepo) RecentPublic(context.Context, uuid.UUID, int) ([]MessageMetaDataRes, error) {
	return nil, nil
}
func (f *fakeRepo) UpdateLastSeen(context.Context, string, uuid.UUID, uuid.UUID) error { return nil }

type fakeGroups map[uuid.UUID][]uuid.UUID

func (g fakeGroups) IsMember(_ context.Context, id, user uuid.UUID) (bool, error) {
	return slices.Contains(g[id], user), nil
}
func (g fakeGroups) Members(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	return slices.Clone(g[id]), nil
}

type fakeFriends map[[2]uuid.UUID]bool

func (f fakeFriends) AreFriends(_ context.Context, a, b uuid.UUID) (bool, error) {
	return f[[2]uuid.UUID{a, b}] || f[[2]uuid.UUID{b, a}], nil
}

type fakePush struct {
	mu      sync.Mutex
	targets [][]uuid.UUID
}

func (p *fakePush) WriteToUsers(t []uuid.UUID, _ ws.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets = append(p.targets, t)
	return nil
}

type countQueue struct{ topics []string }

func (q *countQueue) Publish(_ context.Context, topic string, _ any) error {
	q.topics = append(q.topics, topic)
	return nil
}

type fixture struct {
	svc               ChatService
	repo              *fakeRepo
	push              *fakePush
	queue             *countQueue
	alice, bob, carol uuid.UUID
	group             uuid.UUID
}

func setup(t *testing.T) fixture {
	t.Helper()
	rdb, _ := testutil.Redis(t)
	f := fixture{repo: &fakeRepo{}, push: &fakePush{}, queue: &countQueue{}, alice: uuid.New(), bob: uuid.New(), carol: uuid.New(), group: uuid.New()}
	groups := fakeGroups{f.group: {f.alice, f.bob}}
	friends := fakeFriends{{f.alice, f.bob}: true}
	f.svc = NewChatService(f.repo, NewCache(rdb), groups, friends, f.push, f.queue, testutil.Logger())
	return f
}

func msg(to uuid.UUID, kind, text string) InCommingMessage {
	return InCommingMessage{Content: text, ToID: to.String(), Type: kind}
}

func TestSendValidation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cases := []struct {
		in   InCommingMessage
		want error
	}{
		{msg(f.bob, TypePrivate, ""), ErrEmptyMessage},
		{msg(f.bob, TypePrivate, strings.Repeat("x", maxMessageLen+1)), ErrEmptyMessage},
		{InCommingMessage{Content: "hi", ToID: "nope", Type: TypePrivate}, ErrNotValidToID},
		{msg(f.alice, TypePrivate, "me"), ErrNotValidToID},
		{InCommingMessage{Content: "hi", ToID: f.bob.String(), Type: TypePrivate, ParendID: "x"}, ErrNotValidParentID},
		{msg(f.bob, "broadcast", "hi"), ErrNotSupportedTypeMsg},
		{msg(f.carol, TypePrivate, "hi"), ErrNotFriends},
		{msg(uuid.New(), TypePublic, "hi"), ErrNotAuthorized},
	}
	for _, c := range cases {
		if _, err := f.svc.SendMessage(ctx, f.alice, c.in); !errors.Is(err, c.want) {
			t.Errorf("SendMessage(%+v) err = %v, want %v", c.in, err, c.want)
		}
	}
	if len(f.queue.topics) != 0 {
		t.Fatalf("rejected messages were queued: %v", f.queue.topics)
	}
}

func TestPrivateMessagesKeepHistoryOrder(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	old := time.Now().Add(-time.Hour).UTC()
	f.repo.history = []MessageMetaDataRes{{ID: uuid.New(), CreatedAt: old, MsgInfo: MessageCache{Msg: InCommingMessage{Content: "from postgres"}}}}

	for _, text := range []string{"one", "two"} {
		if _, err := f.svc.SendMessage(ctx, f.alice, msg(f.bob, TypePrivate, text)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.svc.PrivateMessages(ctx, f.bob, f.alice, "")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range got {
		texts = append(texts, m.MsgInfo.Msg.Content)
	}
	if want := []string{"from postgres", "one", "two"}; !slices.Equal(texts, want) {
		t.Fatalf("messages = %v, want %v", texts, want)
	}
	if len(f.push.targets) != 2 || f.push.targets[0][0] != f.bob {
		t.Fatalf("push targets = %v", f.push.targets)
	}
	if f.queue.topics[0] != TopicPrivateMessage {
		t.Fatalf("topic = %v", f.queue.topics)
	}
}

func TestPrivateMessagesSince(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.svc.PrivateMessages(ctx, f.alice, f.bob, "yesterday"); !errors.Is(err, ErrNotValidTimeFmt) {
		t.Fatalf("bad since: %v", err)
	}
	cutoff := time.Now().UTC()
	time.Sleep(2 * time.Millisecond)
	if _, err := f.svc.SendMessage(ctx, f.alice, msg(f.bob, TypePrivate, "new")); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.PrivateMessages(ctx, f.alice, f.bob, cutoff.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].MsgInfo.Msg.Content != "new" {
		t.Fatalf("since result = %+v", got)
	}
	if f.repo.since != 0 {
		t.Fatal("cache covered the range; postgres should not be queried")
	}
}

func TestGroupMessages(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.svc.SendMessage(ctx, f.alice, msg(f.group, TypePublic, "hello group")); err != nil {
		t.Fatal(err)
	}
	if got := f.push.targets[0]; len(got) != 1 || got[0] != f.bob {
		t.Fatalf("fan-out should skip the sender: %v", got)
	}
	got, err := f.svc.GroupMessages(ctx, f.bob, f.group)
	if err != nil || len(got) != 1 {
		t.Fatalf("GroupMessages = %v, %v", got, err)
	}
	if _, err := f.svc.GroupMessages(ctx, f.carol, f.group); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("non-member read: %v", err)
	}
}

func TestMarkSeen(t *testing.T) {
	f := setup(t)
	if err := f.svc.MarkSeen(context.Background(), f.alice, f.bob, "bad"); !errors.Is(err, ErrNotValidUUID) {
		t.Fatalf("err = %v", err)
	}
	if err := f.svc.MarkSeen(context.Background(), f.alice, f.bob, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if f.queue.topics[0] != TopicSeen || f.push.targets[0][0] != f.bob {
		t.Fatalf("topics %v pushes %v", f.queue.topics, f.push.targets)
	}
}

func TestPrivateChatIDIsSymmetric(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if PrivateChatID(a, b) != PrivateChatID(b, a) {
		t.Fatal("chat id must not depend on argument order")
	}
}

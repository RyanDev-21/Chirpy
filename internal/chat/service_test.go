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
	saved   []MessageMetaDataRes
	parents map[uuid.UUID]bool // parent ids that belong to the chat
	failAdd error
}

func (f *fakeRepo) AddPrivate(_ context.Context, m MessageMetaDataRes) error {
	if f.failAdd != nil {
		return f.failAdd
	}
	f.saved = append(f.saved, m)
	return nil
}
func (f *fakeRepo) AddPublic(ctx context.Context, m MessageMetaDataRes) error {
	return f.AddPrivate(ctx, m)
}
func (f *fakeRepo) ParentInPrivate(_ context.Context, p, _, _ uuid.UUID) (bool, error) {
	return f.parents[p], nil
}
func (f *fakeRepo) ParentInGroup(_ context.Context, p, _ uuid.UUID) (bool, error) {
	return f.parents[p], nil
}
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
	f := fixture{repo: &fakeRepo{parents: map[uuid.UUID]bool{}}, push: &fakePush{}, queue: &countQueue{}, alice: uuid.New(), bob: uuid.New(), carol: uuid.New(), group: uuid.New()}
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
	if len(f.repo.saved) != 0 {
		t.Fatalf("rejected messages were saved: %v", f.repo.saved)
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
	if len(f.repo.saved) != 2 || len(f.queue.topics) != 0 {
		t.Fatalf("messages must be saved synchronously, not queued: saved=%d queued=%v", len(f.repo.saved), f.queue.topics)
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
	foreign := uuid.New()
	if err := f.svc.MarkSeen(context.Background(), f.alice, f.bob, foreign.String()); !errors.Is(err, ErrMessageNotInChat) {
		t.Fatalf("receipt for a message outside the chat: %v", err)
	}
	inChat := uuid.New()
	f.repo.parents[inChat] = true
	if err := f.svc.MarkSeen(context.Background(), f.alice, f.bob, inChat.String()); err != nil {
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

func TestParentMustBelongToChat(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	foreign := uuid.New()
	in := msg(f.bob, TypePrivate, "reply")
	in.ParendID = foreign.String()
	if _, err := f.svc.SendMessage(ctx, f.alice, in); !errors.Is(err, ErrNotValidParentID) {
		t.Fatalf("foreign parent: %v", err)
	}
	f.repo.parents[foreign] = true
	if _, err := f.svc.SendMessage(ctx, f.alice, in); err != nil {
		t.Fatalf("valid parent: %v", err)
	}
}

func TestDatabaseFailureFailsSendAndSkipsDelivery(t *testing.T) {
	f := setup(t)
	f.repo.failAdd = errors.New("postgres down")
	if _, err := f.svc.SendMessage(context.Background(), f.alice, msg(f.bob, TypePrivate, "hi")); err == nil {
		t.Fatal("send must fail when postgres rejects the insert")
	}
	if len(f.push.targets) != 0 {
		t.Fatal("an unsaved message must not be delivered")
	}
	got, _ := f.svc.PrivateMessages(context.Background(), f.bob, f.alice, "")
	if len(got) != 0 {
		t.Fatalf("an unsaved message must not be cached: %+v", got)
	}
}

func TestRealtimeEventPolicy(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	// private typing: friends only
	if got, _ := f.svc.TypingTargets(ctx, f.alice, f.bob, false); len(got) != 1 || got[0] != f.bob {
		t.Fatalf("friend typing targets = %v", got)
	}
	if got, _ := f.svc.TypingTargets(ctx, f.carol, f.bob, false); len(got) != 0 {
		t.Fatalf("stranger typing must be dropped, got %v", got)
	}
	// group typing: members only, never echoed to the sender
	if got, _ := f.svc.TypingTargets(ctx, f.alice, f.group, true); len(got) != 1 || got[0] != f.bob {
		t.Fatalf("group typing targets = %v", got)
	}
	if got, _ := f.svc.TypingTargets(ctx, f.carol, f.group, true); len(got) != 0 {
		t.Fatalf("non-member group typing must be dropped, got %v", got)
	}
	if got, err := f.svc.TypingTargets(ctx, f.alice, uuid.New(), true); err != nil || len(got) != 0 {
		t.Fatalf("unknown group: %v %v", got, err)
	}
	// seen receipts: friends only
	if ok, _ := f.svc.SeenAllowed(ctx, f.alice, f.bob); !ok {
		t.Fatal("friends may send read receipts")
	}
	if ok, _ := f.svc.SeenAllowed(ctx, f.carol, f.bob); ok {
		t.Fatal("strangers must not send read receipts")
	}
}

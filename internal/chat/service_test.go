package chat

import (
	"fmt"
	"log/slog"

	"RyanDev-21.com/Chirpy/internal/media"
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
	history     []MessageMetaDataRes
	since       int
	saved       []MessageMetaDataRes
	parents     map[uuid.UUID]bool // parent ids that belong to the chat
	beforeCalls int
	reactions   map[uuid.UUID]map[uuid.UUID]string // msg -> user -> emoji
	failAdd     error
}

func (f *fakeRepo) AddPrivate(_ context.Context, m MessageMetaDataRes) error {
	if f.failAdd != nil {
		return f.failAdd
	}
	f.saved = append(f.saved, m)
	f.history = append(f.history, m)
	return nil
}
func (f *fakeRepo) AddPublic(ctx context.Context, m MessageMetaDataRes) error {
	return f.AddPrivate(ctx, m)
}
func (f *fakeRepo) PrivateBefore(_ context.Context, _, _, before uuid.UUID, n int) ([]MessageMetaDataRes, error) {
	f.beforeCalls++
	return nil, nil
}
func (f *fakeRepo) GroupBefore(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int) ([]MessageMetaDataRes, error) {
	f.beforeCalls++
	return nil, nil
}
func (f *fakeRepo) MarkGroupRead(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}
func (f *fakeRepo) RecentGroupSenders(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}
func (f *fakeRepo) GroupReadBy(context.Context, messageRef, uuid.UUID) ([]Reader, error) {
	return []Reader{}, nil
}
func (f *fakeRepo) Conversations(context.Context, uuid.UUID) ([]Conversation, error) { return nil, nil }
func (f *fakeRepo) GroupSince(context.Context, uuid.UUID, uuid.UUID, time.Time, int) ([]MessageMetaDataRes, error) {
	return nil, nil
}
func (f *fakeRepo) PeerSeen(context.Context, string, uuid.UUID) (uuid.UUID, bool, error) {
	return uuid.Nil, false, nil
}
func (f *fakeRepo) MessageContent(context.Context, string, uuid.UUID) (string, bool, error) {
	return "", false, nil
}
func (f *fakeRepo) LocateMessage(_ context.Context, id uuid.UUID) (messageRef, error) {
	for _, m := range f.saved {
		if m.ID != id {
			continue
		}
		if m.Deleted {
			break
		}
		to, _ := uuid.Parse(m.MsgInfo.Msg.ToID)
		if m.MsgInfo.Msg.Type == TypePublic {
			return messageRef{Kind: TypePublic, Group: to, From: m.MsgInfo.FromID, CreatedAt: m.CreatedAt}, nil
		}
		return messageRef{Kind: TypePrivate, From: m.MsgInfo.FromID, To: to, CreatedAt: m.CreatedAt}, nil
	}
	return messageRef{}, ErrMessageNotFound
}
func (f *fakeRepo) SetReaction(_ context.Context, _ string, msg, user uuid.UUID, emoji string) error {
	if f.reactions == nil {
		f.reactions = map[uuid.UUID]map[uuid.UUID]string{}
	}
	if f.reactions[msg] == nil {
		f.reactions[msg] = map[uuid.UUID]string{}
	}
	f.reactions[msg][user] = emoji
	return nil
}
func (f *fakeRepo) DeleteReaction(_ context.Context, _ string, msg, user uuid.UUID) (bool, error) {
	_, ok := f.reactions[msg][user]
	delete(f.reactions[msg], user)
	return ok, nil
}
func (f *fakeRepo) ReactionCounts(_ context.Context, _ string, me uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]ReactionCount, error) {
	out := map[uuid.UUID][]ReactionCount{}
	for _, id := range ids {
		counts := map[string]*ReactionCount{}
		var order []string
		for user, e := range f.reactions[id] {
			if counts[e] == nil {
				counts[e] = &ReactionCount{Emoji: e}
				order = append(order, e)
			}
			counts[e].Count++
			counts[e].Me = counts[e].Me || user == me
		}
		slices.Sort(order)
		for _, e := range order {
			out[id] = append(out[id], *counts[e])
		}
	}
	return out, nil
}
func (f *fakeRepo) Reactors(context.Context, string, uuid.UUID) ([]Reactor, error) { return nil, nil }
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
	return f.history, nil
}
func (f *fakeRepo) JoinedAt(context.Context, uuid.UUID, uuid.UUID) (time.Time, error) {
	return time.Time{}, nil
}
func (f *fakeRepo) RecentPublic(context.Context, uuid.UUID, uuid.UUID, int) ([]MessageMetaDataRes, error) {
	return f.history, nil
}
func (f *fakeRepo) UpdateLastSeen(context.Context, string, uuid.UUID, uuid.UUID) error { return nil }

func (f *fakeRepo) ForgetBans() {}

func (f *fakeRepo) Suspended(context.Context, []uuid.UUID) (map[uuid.UUID]bool, error) {
	return map[uuid.UUID]bool{}, nil
}

// EditMessage and DeleteMessage change the stored row, like postgres.
func (f *fakeRepo) EditMessage(_ context.Context, _ string, id uuid.UUID, content string, at time.Time) (bool, error) {
	for i := range f.saved {
		if f.saved[i].ID == id && !f.saved[i].Deleted {
			f.saved[i].MsgInfo.Msg.Content, f.saved[i].EditedAt = content, &at
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeRepo) DeleteMessage(_ context.Context, _ string, id uuid.UUID, _ time.Time) (bool, error) {
	for i := range f.saved {
		if f.saved[i].ID == id && !f.saved[i].Deleted {
			f.saved[i].MsgInfo.Msg.Content, f.saved[i].Deleted = "", true
			return true, nil
		}
	}
	return false, nil
}

type fakeGroups map[uuid.UUID][]uuid.UUID

func (g fakeGroups) IsMember(_ context.Context, id, user uuid.UUID) (bool, error) {
	return slices.Contains(g[id], user), nil
}
func (g fakeGroups) CanDeleteMessage(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
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
	got, err := f.svc.PrivateMessages(ctx, f.bob, f.alice, HistoryQuery{})
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
	if _, err := f.svc.PrivateMessages(ctx, f.alice, f.bob, HistoryQuery{Since: "yesterday"}); !errors.Is(err, ErrNotValidTimeFmt) {
		t.Fatalf("bad since: %v", err)
	}
	cutoff := time.Now().UTC()
	time.Sleep(2 * time.Millisecond)
	if _, err := f.svc.SendMessage(ctx, f.alice, msg(f.bob, TypePrivate, "new")); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.PrivateMessages(ctx, f.alice, f.bob, HistoryQuery{Since: cutoff.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].MsgInfo.Msg.Content != "new" {
		t.Fatalf("since result = %+v", got)
	}
	if f.repo.since != 1 {
		t.Fatalf("PrivateSince calls = %d, want 1", f.repo.since)
	}
}

func TestGroupMessages(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.svc.SendMessage(ctx, f.alice, msg(f.group, TypePublic, "hello group")); err != nil {
		t.Fatal(err)
	}
	// members, then the sender's own devices
	if got := f.push.targets[0]; len(got) != 2 || got[0] != f.bob || got[1] != f.alice {
		t.Fatalf("fan-out: %v", got)
	}
	got, err := f.svc.GroupMessages(ctx, f.bob, f.group, HistoryQuery{})
	if err != nil || len(got) != 1 {
		t.Fatalf("GroupMessages = %v, %v", got, err)
	}
	if _, err := f.svc.GroupMessages(ctx, f.carol, f.group, HistoryQuery{}); !errors.Is(err, ErrNotAuthorized) {
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
	got, _ := f.svc.PrivateMessages(context.Background(), f.bob, f.alice, HistoryQuery{})
	if len(got) != 0 {
		t.Fatalf("an unsaved message must not be returned: %+v", got)
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

func TestHistoryQueryRules(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.svc.PrivateMessages(ctx, f.alice, f.bob, HistoryQuery{Since: "2026-01-01T00:00:00Z", Before: uuid.NewString()}); !errors.Is(err, ErrSinceAndBefore) {
		t.Fatalf("since+before: %v", err)
	}
	if _, err := f.svc.PrivateMessages(ctx, f.alice, f.bob, HistoryQuery{Before: "nope"}); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("bad cursor: %v", err)
	}
	if _, err := f.svc.GroupMessages(ctx, f.alice, f.group, HistoryQuery{Since: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatalf("group since: %v", err)
	}
	if _, err := f.svc.GroupMessages(ctx, f.alice, f.group, HistoryQuery{Since: "yesterday"}); !errors.Is(err, ErrNotValidTimeFmt) {
		t.Fatalf("group since, bad time: %v", err)
	}
	if _, err := f.svc.GroupMessages(ctx, f.carol, f.group, HistoryQuery{Before: uuid.NewString()}); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("non-member paging: %v", err)
	}
	// older pages come from postgres
	_, _ = f.svc.PrivateMessages(ctx, f.alice, f.bob, HistoryQuery{Before: uuid.NewString()})
	_, _ = f.svc.GroupMessages(ctx, f.alice, f.group, HistoryQuery{Before: uuid.NewString()})
	if f.repo.beforeCalls != 2 {
		t.Fatalf("before calls = %d, want 2", f.repo.beforeCalls)
	}
	if _, limit, _ := (HistoryQuery{Limit: 5000}).parse(); limit != maxPage {
		t.Fatalf("limit not capped: %d", limit)
	}
	if err := f.svc.MarkGroupRead(ctx, f.carol, f.group, uuid.NewString()); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("non-member read marker: %v", err)
	}
}

func TestReactions(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	dm, _ := f.svc.SendMessage(ctx, f.alice, msg(f.bob, TypePrivate, "react to me"))
	gm, _ := f.svc.SendMessage(ctx, f.alice, msg(f.group, TypePublic, "group msg"))

	if err := f.svc.React(ctx, f.bob, dm, "ok"); !errors.Is(err, ErrNotAnEmoji) {
		t.Fatalf("text as emoji: %v", err)
	}
	if err := f.svc.React(ctx, f.carol, dm, "👍"); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("outsider on a private message: %v", err)
	}
	if err := f.svc.React(ctx, f.carol, gm, "👍"); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("non-member on a group message: %v", err)
	}
	if err := f.svc.React(ctx, f.bob, uuid.New(), "👍"); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("unknown message: %v", err)
	}

	pushes := len(f.push.targets)
	if err := f.svc.React(ctx, f.bob, dm, "👍"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.React(ctx, f.bob, dm, "❤️"); err != nil { // replaces, one per person
		t.Fatal(err)
	}
	_ = f.svc.React(ctx, f.alice, dm, "❤️")
	if got := f.push.targets[pushes]; len(got) != 2 || got[0] != f.alice || got[1] != f.bob {
		t.Fatalf("bob's reaction should reach alice and bob's other devices: %v", got)
	}

	page, err := f.svc.PrivateMessages(ctx, f.bob, f.alice, HistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var r []ReactionCount
	for _, m := range page {
		if m.ID == dm {
			r = m.Reactions
		}
	}
	if len(r) != 1 || r[0].Emoji != "❤️" || r[0].Count != 2 || !r[0].Me {
		t.Fatalf("reactions in history = %+v", r)
	}

	if err := f.svc.Unreact(ctx, f.bob, dm); err != nil {
		t.Fatal(err)
	}
	if n := len(f.push.targets); f.svc.Unreact(ctx, f.bob, dm) != nil || len(f.push.targets) != n {
		t.Fatal("removing a missing reaction must be a quiet no-op")
	}
	// group: members react, the rest of the group hears it
	if err := f.svc.React(ctx, f.bob, gm, "🔥"); err != nil {
		t.Fatal(err)
	}
	if got := f.push.targets[len(f.push.targets)-1]; len(got) != 2 || got[0] != f.alice || got[1] != f.bob {
		t.Fatalf("group reaction targets = %v", got)
	}
}

func TestEditAndDelete(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	dm, _ := f.svc.SendMessage(ctx, f.alice, msg(f.bob, TypePrivate, "helo"))
	gm, _ := f.svc.SendMessage(ctx, f.alice, msg(f.group, TypePublic, "group"))
	f.repo.history = f.repo.saved[:1]

	if _, err := f.svc.EditMessage(ctx, f.bob, dm, "hijack"); !errors.Is(err, ErrNotYourMessage) {
		t.Fatalf("edit someone else's message: %v", err)
	}
	if _, err := f.svc.EditMessage(ctx, f.carol, gm, "x"); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("non-member edit: %v", err)
	}
	if _, err := f.svc.EditMessage(ctx, f.alice, dm, "  "); !errors.Is(err, ErrEmptyMessage) {
		t.Fatalf("empty edit: %v", err)
	}
	pushes := len(f.push.targets)
	got, err := f.svc.EditMessage(ctx, f.alice, dm, " hello ")
	if err != nil || got.Content != "hello" {
		t.Fatalf("edit = %+v %v", got, err)
	}
	if tg := f.push.targets[pushes]; len(tg) != 2 || tg[0] != f.bob || tg[1] != f.alice {
		t.Fatalf("edit pushed to %v, want bob and alice's devices", tg)
	}
	page, _ := f.svc.PrivateMessages(ctx, f.bob, f.alice, HistoryQuery{})
	if last := page[len(page)-1]; last.MsgInfo.Msg.Content != "hello" || last.EditedAt == nil {
		t.Fatalf("after edit: %+v", last)
	}

	// the edit window is measured from sending
	f.repo.saved[0].CreatedAt = time.Now().UTC().Add(-EditWindow - time.Second)
	if _, err := f.svc.EditMessage(ctx, f.alice, dm, "late"); !errors.Is(err, ErrEditWindowClosed) {
		t.Fatalf("late edit: %v", err)
	}

	if err := f.svc.DeleteMessage(ctx, f.bob, dm); !errors.Is(err, ErrNotYourMessage) {
		t.Fatalf("delete someone else's message: %v", err)
	}
	if err := f.svc.DeleteMessage(ctx, f.alice, dm); err != nil {
		t.Fatal(err)
	}
	page, _ = f.svc.PrivateMessages(ctx, f.bob, f.alice, HistoryQuery{})
	if last := page[len(page)-1]; !last.Deleted || last.MsgInfo.Msg.Content != "" {
		t.Fatalf("after delete: %+v", last)
	}
	if err := f.svc.DeleteMessage(ctx, f.alice, dm); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := f.svc.React(ctx, f.bob, dm, "👍"); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("react to deleted: %v", err)
	}
}

func TestForwardErrorsDontLeakInternals(t *testing.T) {
	s := &chatService{logger: slog.New(slog.DiscardHandler)}
	if got := s.forwardError(context.Background(), ErrNotFriends); got != ErrNotFriends.Error() {
		t.Fatalf("known error became %q", got)
	}
	if got := s.forwardError(context.Background(), fmt.Errorf("wrapped: %w", media.ErrForwardExpired)); got != media.ErrForwardExpired.Error() {
		t.Fatalf("wrapped known error became %q", got)
	}
	internal := errors.New(`ERROR: relation "message" does not exist (SQLSTATE 42P01) host=10.0.0.3`)
	if got := s.forwardError(context.Background(), internal); strings.Contains(got, "SQLSTATE") || strings.Contains(got, "10.0.0.3") {
		t.Fatalf("internal error leaked: %q", got)
	}
}

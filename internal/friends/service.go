package friends

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"RyanDev-21.com/Chirpy/internal/push"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
)

// NameLookup resolves display names (implemented by users.UserService).
type NameLookup interface {
	GetName(ctx context.Context, id uuid.UUID) (string, error)
}

// Notifier pushes realtime events (implemented by *ws.Hub).
type Notifier interface {
	WriteIntoConnection(userID uuid.UUID, ev ws.Event) error
}

type Service interface {
	Send(ctx context.Context, fromID, toID uuid.UUID) (uuid.UUID, error)
	Accept(ctx context.Context, me, reqID uuid.UUID) error
	Deny(ctx context.Context, me, reqID uuid.UUID) error
	Cancel(ctx context.Context, me, reqID uuid.UUID) error
	Relationships(ctx context.Context, me uuid.UUID) (Snapshot, error)
	AreFriends(ctx context.Context, a, b uuid.UUID) (bool, error)
	Unfriend(ctx context.Context, me, other uuid.UUID) error
	// Block ends any friendship or request with other and stops new ones
	// (and search) both ways. other is not told about the block.
	Block(ctx context.Context, me, other uuid.UUID) error
	Unblock(ctx context.Context, me, other uuid.UUID) error
	Blocked(ctx context.Context, me uuid.UUID) ([]BlockedUser, error)
	// ForgetNames drops cached relationship lists that show userID's name
	// (theirs, their friends' and their requests'), after a rename.
	ForgetNames(ctx context.Context, userID uuid.UUID)
	// OnBreak registers a hook for a friendship ending (unfriend or block).
	OnBreak(fn func(ctx context.Context, a, b uuid.UUID))
	// SetNotifier enables push notifications for requests and acceptances.
	SetNotifier(n push.Notifier)
}

type service struct {
	repo   Repo
	store  Store
	names  NameLookup
	notify Notifier
	alerts push.Notifier // nil: no push notifications
	logger *slog.Logger
	breaks []func(ctx context.Context, a, b uuid.UUID)
}

func (s *service) OnBreak(fn func(ctx context.Context, a, b uuid.UUID)) {
	s.breaks = append(s.breaks, fn)
}

func (s *service) broke(ctx context.Context, a, b uuid.UUID) {
	for _, fn := range s.breaks {
		fn(ctx, a, b)
	}
}

func (s *service) SetNotifier(n push.Notifier) { s.alerts = n }

func NewService(repo Repo, store Store, names NameLookup, notify Notifier, logger *slog.Logger) Service {
	return &service{repo: repo, store: store, names: names, notify: notify, logger: logger}
}

// Every write goes to postgres first; the cache is then invalidated for
// both users and rebuilt on their next read.

func (s *service) Send(ctx context.Context, fromID, toID uuid.UUID) (uuid.UUID, error) {
	if fromID == toID {
		return uuid.Nil, ErrSelfRequest
	}
	if _, err := s.names.GetName(ctx, toID); err != nil {
		if errors.Is(err, users.ErrUserNotFound) {
			return uuid.Nil, ErrUnknownUser
		}
		return uuid.Nil, err
	}
	iBlocked, theyBlocked, err := s.repo.BlockBetween(ctx, fromID, toID)
	if err != nil {
		return uuid.Nil, err
	}
	if iBlocked {
		return uuid.Nil, ErrYouBlocked
	}
	if theyBlocked {
		return uuid.Nil, ErrUnknownUser // same answer as a user that doesn't exist
	}
	reqID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.repo.Create(ctx, reqID, fromID, toID); err != nil {
		return uuid.Nil, err
	}
	s.invalidate(ctx, fromID, toID)
	s.push(ctx, toID, ws.EventAddFriend, reqID, fromID)
	if s.alerts != nil {
		s.alerts.Notify(ctx, push.Job{Kind: push.KindFriendRequest, To: []uuid.UUID{toID}, From: fromID})
	}
	return reqID, nil
}

// Accept confirms a request that `me` received.
func (s *service) Accept(ctx context.Context, me, reqID uuid.UUID) error {
	senderID, err := s.repo.Accept(ctx, reqID, me)
	if err != nil {
		return err
	}
	s.invalidate(ctx, me, senderID)
	s.push(ctx, senderID, ws.EventAccept, reqID, me)
	if s.alerts != nil {
		s.alerts.Notify(ctx, push.Job{Kind: push.KindFriendAccept, To: []uuid.UUID{senderID}, From: me})
	}
	return nil
}

// Deny rejects a request that `me` received.
func (s *service) Deny(ctx context.Context, me, reqID uuid.UUID) error {
	senderID, err := s.repo.Deny(ctx, reqID, me)
	if err != nil {
		return err
	}
	s.invalidate(ctx, me, senderID)
	s.push(ctx, senderID, ws.EventDeny, reqID, me)
	return nil
}

// Cancel withdraws a request that `me` sent.
func (s *service) Cancel(ctx context.Context, me, reqID uuid.UUID) error {
	receiverID, err := s.repo.Cancel(ctx, reqID, me)
	if err != nil {
		return err
	}
	s.invalidate(ctx, me, receiverID)
	return nil
}

// Unfriend removes the friendship for both users. The other user is told
// over the socket; chat history stays, but new messages need a new request.
func (s *service) Unfriend(ctx context.Context, me, other uuid.UUID) error {
	if err := s.repo.Unfriend(ctx, me, other); err != nil {
		return err
	}
	s.invalidate(ctx, me, other)
	s.broke(ctx, me, other)
	if err := s.notify.WriteIntoConnection(other, ws.Event{Event: ws.EventUnfriend, Payload: ws.OutUnfriendEvent{FromID: me.String()}}); err != nil {
		s.logger.WarnContext(ctx, "failed to push unfriend event", "error", err)
	}
	return nil
}

func (s *service) Block(ctx context.Context, me, other uuid.UUID) error {
	if me == other {
		return ErrSelfBlock
	}
	wereFriends, err := s.repo.Block(ctx, me, other, time.Now().UTC())
	if err != nil {
		return err
	}
	s.invalidate(ctx, me, other)
	s.broke(ctx, me, other)
	// they see the friendship end, as with an unfriend, but not the block
	if wereFriends {
		if err := s.notify.WriteIntoConnection(other, ws.Event{Event: ws.EventUnfriend, Payload: ws.OutUnfriendEvent{FromID: me.String()}}); err != nil {
			s.logger.WarnContext(ctx, "failed to push unfriend event", "error", err)
		}
	}
	return nil
}

func (s *service) Unblock(ctx context.Context, me, other uuid.UUID) error {
	return s.repo.Unblock(ctx, me, other)
}

func (s *service) Blocked(ctx context.Context, me uuid.UUID) ([]BlockedUser, error) {
	return s.repo.Blocked(ctx, me)
}

func (s *service) ForgetNames(ctx context.Context, userID uuid.UUID) {
	snap, err := s.repo.Snapshot(ctx, userID)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to load relationships after rename", "error", err)
		s.invalidate(ctx, userID)
		return
	}
	ids := []uuid.UUID{userID}
	for _, f := range snap.Friends {
		ids = append(ids, f.UserID)
	}
	for _, m := range []map[uuid.UUID]FriendMetaData{snap.Incoming, snap.Outgoing} {
		for _, f := range m {
			ids = append(ids, f.UserID)
		}
	}
	s.invalidate(ctx, ids...)
}

func (s *service) Relationships(ctx context.Context, me uuid.UUID) (Snapshot, error) {
	return s.store.Load(ctx, me.String(), func(ctx context.Context) (Snapshot, error) {
		return s.repo.Snapshot(ctx, me)
	}, func(err error) { s.logger.WarnContext(ctx, "friends cache error", "error", err) })
}

func (s *service) AreFriends(ctx context.Context, a, b uuid.UUID) (bool, error) {
	snap, err := s.Relationships(ctx, a)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(snap.Friends, func(f FriendMetaData) bool { return f.UserID == b }), nil
}

func (s *service) invalidate(ctx context.Context, ids ...uuid.UUID) {
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = id.String()
	}
	if err := s.store.Invalidate(ctx, keys...); err != nil {
		// the write is committed; a stale cache entry expires with its TTL
		s.logger.ErrorContext(ctx, "failed to invalidate friends cache", "error", err)
	}
}

// push is best effort: offline users see the change on their next fetch.
func (s *service) push(ctx context.Context, to uuid.UUID, event string, reqID, fromID uuid.UUID) {
	if err := s.notify.WriteIntoConnection(to, ws.Event{
		Event:   event,
		Payload: ws.OutFriEvent{ReqID: reqID.String(), FromID: fromID.String()},
	}); err != nil {
		s.logger.WarnContext(ctx, "failed to push friend event", "event", event, "error", err)
	}
}

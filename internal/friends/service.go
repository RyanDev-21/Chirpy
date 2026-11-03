package friends

import (
	"context"
	"errors"
	"log/slog"
	"slices"

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
}

type service struct {
	repo   Repo
	store  Store
	names  NameLookup
	notify Notifier
	logger *slog.Logger
}

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
		if errors.Is(err, users.NoUserFoundErr) {
			return uuid.Nil, ErrUnknownUser
		}
		return uuid.Nil, err
	}
	reqID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.repo.Create(ctx, reqID, fromID, toID); err != nil {
		return uuid.Nil, err
	}
	s.invalidate(ctx, fromID, toID)
	s.push(toID, ws.EventAddFriend, reqID, fromID)
	return reqID, nil
}

// Accept confirms a request that `me` received.
func (s *service) Accept(ctx context.Context, me, reqID uuid.UUID) error {
	senderID, err := s.repo.Accept(ctx, reqID, me)
	if err != nil {
		return err
	}
	s.invalidate(ctx, me, senderID)
	s.push(senderID, ws.EventAccept, reqID, me)
	return nil
}

// Deny rejects a request that `me` received.
func (s *service) Deny(ctx context.Context, me, reqID uuid.UUID) error {
	senderID, err := s.repo.Deny(ctx, reqID, me)
	if err != nil {
		return err
	}
	s.invalidate(ctx, me, senderID)
	s.push(senderID, ws.EventDeny, reqID, me)
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

func (s *service) Relationships(ctx context.Context, me uuid.UUID) (Snapshot, error) {
	snap, ok, err := s.store.Get(ctx, me)
	if err != nil {
		s.logger.Warn("friends cache read failed", "error", err)
	}
	if ok {
		return snap, nil
	}
	version, err := s.store.Version(ctx, me)
	if err != nil {
		s.logger.Warn("friends cache version read failed", "error", err)
	}
	snap, err = s.repo.Snapshot(ctx, me)
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.store.Put(ctx, me, version, snap); err != nil {
		s.logger.Warn("failed to cache relationships", "error", err)
	}
	return snap, nil
}

func (s *service) AreFriends(ctx context.Context, a, b uuid.UUID) (bool, error) {
	snap, err := s.Relationships(ctx, a)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(snap.Friends, func(f FriendMetaData) bool { return f.UserID == b }), nil
}

func (s *service) invalidate(ctx context.Context, ids ...uuid.UUID) {
	if err := s.store.Invalidate(ctx, ids...); err != nil {
		// the write is committed; a stale cache entry expires with its TTL
		s.logger.Error("failed to invalidate friends cache", "error", err)
	}
}

// push is best effort: offline users see the change on their next fetch.
func (s *service) push(to uuid.UUID, event string, reqID, fromID uuid.UUID) {
	if err := s.notify.WriteIntoConnection(to, ws.Event{
		Event:   event,
		Payload: ws.OutFriEvent{ReqID: reqID.String(), FromID: fromID.String()},
	}); err != nil {
		s.logger.Warn("failed to push friend event", "event", event, "error", err)
	}
}

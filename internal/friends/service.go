package friends

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
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

	HandleCreate(ctx context.Context, j JobRequest) error
	HandleAccept(ctx context.Context, j JobRequest) error
	HandleDeny(ctx context.Context, j JobRequest) error
	HandleCancel(ctx context.Context, j JobRequest) error
}

type service struct {
	repo   Repo
	store  Store
	names  NameLookup
	notify Notifier
	queue  queue.Publisher
	logger *slog.Logger
}

func NewService(repo Repo, store Store, names NameLookup, notify Notifier, q queue.Publisher, logger *slog.Logger) Service {
	return &service{repo: repo, store: store, names: names, notify: notify, queue: q, logger: logger}
}

// ensureLoaded copies a user's relationships from postgres into redis the
// first time they are needed.
func (s *service) ensureLoaded(ctx context.Context, userID uuid.UUID) error {
	ok, err := s.store.Loaded(ctx, userID)
	if err != nil || ok {
		return err
	}
	snap, err := s.repo.Snapshot(ctx, userID)
	if err != nil {
		return fmt.Errorf("load relationships: %w", err)
	}
	return s.store.Load(ctx, userID, snap)
}

func (s *service) member(ctx context.Context, id uuid.UUID) (FriendMetaData, error) {
	name, err := s.names.GetName(ctx, id)
	if errors.Is(err, users.NoUserFoundErr) {
		return FriendMetaData{}, ErrUnknownUser
	}
	if err != nil {
		return FriendMetaData{}, err
	}
	return FriendMetaData{UserID: id, Name: name}, nil
}

func (s *service) Send(ctx context.Context, fromID, toID uuid.UUID) (uuid.UUID, error) {
	if fromID == toID {
		return uuid.Nil, ErrSelfRequest
	}
	from, err := s.member(ctx, fromID)
	if err != nil {
		return uuid.Nil, err
	}
	to, err := s.member(ctx, toID)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.ensureLoaded(ctx, fromID); err != nil {
		return uuid.Nil, err
	}
	if err := s.ensureLoaded(ctx, toID); err != nil {
		return uuid.Nil, err
	}
	reqID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.store.AddRequest(ctx, reqID, from, to); err != nil {
		return uuid.Nil, err
	}
	if err := s.publish(ctx, TopicCreate, JobRequest{ReqID: reqID, FromID: fromID, ToID: toID}); err != nil {
		return uuid.Nil, err
	}
	s.push(toID, ws.EventAddFriend, reqID, fromID)
	return reqID, nil
}

// Accept confirms a request that `me` received.
func (s *service) Accept(ctx context.Context, me, reqID uuid.UUID) error {
	return s.resolve(ctx, me, reqID, true, TopicAccept, ws.EventAccept)
}

// Deny rejects a request that `me` received.
func (s *service) Deny(ctx context.Context, me, reqID uuid.UUID) error {
	return s.resolve(ctx, me, reqID, false, TopicDeny, ws.EventDeny)
}

func (s *service) resolve(ctx context.Context, me, reqID uuid.UUID, accept bool, topic, event string) error {
	if err := s.ensureLoaded(ctx, me); err != nil {
		return err
	}
	sender, ok, err := s.store.Request(ctx, me, reqID, true)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if err := s.ensureLoaded(ctx, sender.UserID); err != nil {
		return err
	}
	receiver, err := s.member(ctx, me)
	if err != nil {
		return err
	}
	if err := s.store.ResolveRequest(ctx, reqID, sender, receiver, accept); err != nil {
		return err
	}
	if err := s.publish(ctx, topic, JobRequest{ReqID: reqID, FromID: sender.UserID, ToID: me}); err != nil {
		return err
	}
	s.push(sender.UserID, event, reqID, me)
	return nil
}

// Cancel withdraws a request that `me` sent.
func (s *service) Cancel(ctx context.Context, me, reqID uuid.UUID) error {
	if err := s.ensureLoaded(ctx, me); err != nil {
		return err
	}
	receiver, ok, err := s.store.Request(ctx, me, reqID, false)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if err := s.ensureLoaded(ctx, receiver.UserID); err != nil {
		return err
	}
	if err := s.store.ResolveRequest(ctx, reqID, FriendMetaData{UserID: me}, receiver, false); err != nil {
		return err
	}
	return s.publish(ctx, TopicCancel, JobRequest{ReqID: reqID, FromID: me, ToID: receiver.UserID})
}

func (s *service) Relationships(ctx context.Context, me uuid.UUID) (Snapshot, error) {
	if err := s.ensureLoaded(ctx, me); err != nil {
		return Snapshot{}, err
	}
	return s.store.Get(ctx, me)
}

func (s *service) AreFriends(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if err := s.ensureLoaded(ctx, a); err != nil {
		return false, err
	}
	return s.store.IsFriend(ctx, a, b)
}

func (s *service) publish(ctx context.Context, topic string, j JobRequest) error {
	pubCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return s.queue.Publish(pubCtx, topic, j)
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

// --- workers: persist the change to postgres ---

var errNotPersistedYet = errors.New("friend request not in postgres yet")

func (s *service) HandleCreate(ctx context.Context, j JobRequest) error {
	err := s.repo.Create(ctx, j)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23503") {
		// duplicate or user deleted: retrying cannot succeed
		return queue.Permanent(err)
	}
	return err
}

// the create job may still be queued when these run, so zero affected rows
// is retried rather than dropped.
func (s *service) HandleAccept(ctx context.Context, j JobRequest) error {
	return rowsOrRetry(s.repo.Accept(ctx, j.ReqID, j.ToID))
}

func (s *service) HandleDeny(ctx context.Context, j JobRequest) error {
	return rowsOrRetry(s.repo.DeleteByReceiver(ctx, j.ReqID, j.ToID))
}

func (s *service) HandleCancel(ctx context.Context, j JobRequest) error {
	return rowsOrRetry(s.repo.DeleteBySender(ctx, j.ReqID, j.FromID))
}

func rowsOrRetry(n int64, err error) error {
	if err != nil {
		return err
	}
	if n == 0 {
		return errNotPersistedYet
	}
	return nil
}

package friends

import (
	"context"
	"errors"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Repo interface {
	Snapshot(ctx context.Context, userID uuid.UUID) (Snapshot, error)
	// Create fails with ErrRequestExists or ErrAlreadyFriends when the pair
	// already has a relationship in either direction.
	Create(ctx context.Context, reqID, fromID, toID uuid.UUID) error
	// Accept, Deny and Cancel return ErrNotFound unless the request is
	// pending and the caller is on the right side of it. They return the
	// other user's id.
	Accept(ctx context.Context, reqID, receiverID uuid.UUID) (uuid.UUID, error)
	Deny(ctx context.Context, reqID, receiverID uuid.UUID) (uuid.UUID, error)
	Cancel(ctx context.Context, reqID, senderID uuid.UUID) (uuid.UUID, error)
	// Unfriend removes a confirmed friendship; ErrNotFriends if there is none.
	Unfriend(ctx context.Context, a, b uuid.UUID) error
}

type repo struct {
	q *database.Queries
}

func NewRepo(q *database.Queries) Repo { return &repo{q: q} }

func (r *repo) Snapshot(ctx context.Context, userID uuid.UUID) (Snapshot, error) {
	snap := Snapshot{
		Friends:  []FriendMetaData{},
		Incoming: map[uuid.UUID]FriendMetaData{},
		Outgoing: map[uuid.UUID]FriendMetaData{},
	}
	friends, err := r.q.ListFriends(ctx, userID)
	if err != nil {
		return snap, err
	}
	for _, f := range friends {
		snap.Friends = append(snap.Friends, FriendMetaData{UserID: f.FriendID, Name: f.FriendName})
	}
	in, err := r.q.ListIncomingFriendRequests(ctx, userID)
	if err != nil {
		return snap, err
	}
	for _, v := range in {
		snap.Incoming[v.ID] = FriendMetaData{UserID: v.FromID, Name: v.FromName}
	}
	out, err := r.q.ListOutgoingFriendRequests(ctx, userID)
	if err != nil {
		return snap, err
	}
	for _, v := range out {
		snap.Outgoing[v.ID] = FriendMetaData{UserID: v.ToID, Name: v.ToName}
	}
	return snap, nil
}

func (r *repo) Create(ctx context.Context, reqID, fromID, toID uuid.UUID) error {
	err := r.q.CreateFriendRequest(ctx, database.CreateFriendRequestParams{ID: reqID, UserID: fromID, OtheruserID: toID})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23505": // user_relationships_pair_key
		status, serr := r.q.PairStatus(ctx, database.PairStatusParams{A: fromID, B: toID})
		if serr == nil && status == "confirm" {
			return ErrAlreadyFriends
		}
		return ErrRequestExists
	case "23503":
		return ErrUnknownUser
	case "23514":
		return ErrSelfRequest
	}
	return err
}

func notFound(id uuid.UUID, err error) (uuid.UUID, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

func (r *repo) Accept(ctx context.Context, reqID, receiverID uuid.UUID) (uuid.UUID, error) {
	return notFound(r.q.AcceptFriendRequest(ctx, database.AcceptFriendRequestParams{ID: reqID, OtheruserID: receiverID}))
}

func (r *repo) Deny(ctx context.Context, reqID, receiverID uuid.UUID) (uuid.UUID, error) {
	return notFound(r.q.DenyFriendRequest(ctx, database.DenyFriendRequestParams{ID: reqID, OtheruserID: receiverID}))
}

func (r *repo) Cancel(ctx context.Context, reqID, senderID uuid.UUID) (uuid.UUID, error) {
	return notFound(r.q.CancelFriendRequest(ctx, database.CancelFriendRequestParams{ID: reqID, UserID: senderID}))
}

func (r *repo) Unfriend(ctx context.Context, a, b uuid.UUID) error {
	_, err := r.q.Unfriend(ctx, database.UnfriendParams{A: a, B: b})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFriends
	}
	return err
}

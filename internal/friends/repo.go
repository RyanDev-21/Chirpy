package friends

import (
	"RyanDev-21.com/Chirpy/internal/users"
	"context"
	"errors"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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

	// Block records that blocker blocked blocked and removes any friendship
	// or request between them, reporting whether they were friends.
	// ErrUnknownUser if blocked doesn't exist. Blocking twice is a no-op.
	Block(ctx context.Context, blocker, blocked uuid.UUID, at time.Time) (wereFriends bool, err error)
	Unblock(ctx context.Context, blocker, blocked uuid.UUID) error
	Blocked(ctx context.Context, blocker uuid.UUID) ([]BlockedUser, error)
	// BlockBetween reports whether a blocked b, and whether b blocked a.
	BlockBetween(ctx context.Context, a, b uuid.UUID) (aBlocked, bBlocked bool, err error)
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
		snap.Friends = append(snap.Friends, FriendMetaData{UserID: f.FriendID, Name: f.FriendName, DisplayName: users.ShownName(f.FriendName, f.FriendDisplay), AvatarURL: users.AvatarURLFrom(f.FriendID, f.FriendAvatarAt)})
	}
	in, err := r.q.ListIncomingFriendRequests(ctx, userID)
	if err != nil {
		return snap, err
	}
	for _, v := range in {
		snap.Incoming[v.ID] = FriendMetaData{UserID: v.FromID, Name: v.FromName, DisplayName: users.ShownName(v.FromName, v.FromDisplay), AvatarURL: users.AvatarURLFrom(v.FromID, v.FromAvatarAt)}
	}
	out, err := r.q.ListOutgoingFriendRequests(ctx, userID)
	if err != nil {
		return snap, err
	}
	for _, v := range out {
		snap.Outgoing[v.ID] = FriendMetaData{UserID: v.ToID, Name: v.ToName, DisplayName: users.ShownName(v.ToName, v.ToDisplay), AvatarURL: users.AvatarURLFrom(v.ToID, v.ToAvatarAt)}
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

func (r *repo) Block(ctx context.Context, blocker, blocked uuid.UUID, at time.Time) (bool, error) {
	were, err := r.q.BlockUser(ctx, database.BlockUserParams{Blocker: blocker, Blocked: blocked, At: pgtype.Timestamp{Time: at, Valid: true}})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return false, ErrUnknownUser
	}
	return were, err
}

func (r *repo) Unblock(ctx context.Context, blocker, blocked uuid.UUID) error {
	return r.q.UnblockUser(ctx, database.UnblockUserParams{BlockerID: blocker, BlockedID: blocked})
}

func (r *repo) Blocked(ctx context.Context, blocker uuid.UUID) ([]BlockedUser, error) {
	rows, err := r.q.ListBlocked(ctx, blocker)
	if err != nil {
		return nil, err
	}
	out := make([]BlockedUser, 0, len(rows))
	for _, x := range rows {
		out = append(out, BlockedUser{UserID: x.BlockedID, Name: x.Name, CreatedAt: x.CreatedAt.Time.UTC()})
	}
	return out, nil
}

func (r *repo) BlockBetween(ctx context.Context, a, b uuid.UUID) (bool, bool, error) {
	row, err := r.q.BlockBetween(ctx, database.BlockBetweenParams{A: a, B: b})
	return row.ABlocked, row.BBlocked, err
}

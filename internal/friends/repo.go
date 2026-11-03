package friends

import (
	"context"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

type Repo interface {
	Snapshot(ctx context.Context, userID uuid.UUID) (Snapshot, error)
	Create(ctx context.Context, j JobRequest) error
	Accept(ctx context.Context, reqID, receiverID uuid.UUID) (int64, error)
	DeleteBySender(ctx context.Context, reqID, senderID uuid.UUID) (int64, error)
	DeleteByReceiver(ctx context.Context, reqID, receiverID uuid.UUID) (int64, error)
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

func (r *repo) Create(ctx context.Context, j JobRequest) error {
	return r.q.CreateFriendRequest(ctx, database.CreateFriendRequestParams{ID: j.ReqID, UserID: j.FromID, OtheruserID: j.ToID})
}

func (r *repo) Accept(ctx context.Context, reqID, receiverID uuid.UUID) (int64, error) {
	return r.q.AcceptFriendRequest(ctx, database.AcceptFriendRequestParams{ID: reqID, OtheruserID: receiverID})
}

func (r *repo) DeleteBySender(ctx context.Context, reqID, senderID uuid.UUID) (int64, error) {
	return r.q.DeleteFriendRequestBySender(ctx, database.DeleteFriendRequestBySenderParams{ID: reqID, UserID: senderID})
}

func (r *repo) DeleteByReceiver(ctx context.Context, reqID, receiverID uuid.UUID) (int64, error) {
	return r.q.DeleteFriendRequestByReceiver(ctx, database.DeleteFriendRequestByReceiverParams{ID: reqID, OtheruserID: receiverID})
}

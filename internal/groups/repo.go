package groups

import (
	"context"
	"errors"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GroupSnapshot is what the store caches about one group.
type GroupSnapshot struct {
	Name     string
	MaxMem   int16
	LeaderID uuid.UUID
	Members  []uuid.UUID
}

type GroupRepo interface {
	CreateWithLeader(ctx context.Context, j JobCreate) error
	NameTaken(ctx context.Context, name string) (bool, error)
	Snapshot(ctx context.Context, groupID uuid.UUID) (GroupSnapshot, error)
	AddMember(ctx context.Context, groupID, userID uuid.UUID) error
	// RemoveMember reports whether a membership row was deleted.
	RemoveMember(ctx context.Context, groupID, userID uuid.UUID) (bool, error)
}

type groupRepo struct {
	pool *pgxpool.Pool
	q    *database.Queries
}

func NewGroupRepo(pool *pgxpool.Pool) GroupRepo {
	return &groupRepo{pool: pool, q: database.New(pool)}
}

// CreateWithLeader inserts the group and its leader in one transaction.
// Both statements are idempotent so the job can be retried safely.
func (r *groupRepo) CreateWithLeader(ctx context.Context, j JobCreate) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		if err := q.CreateGroup(ctx, database.CreateGroupParams{
			ID:            j.GroupID,
			Name:          j.Name,
			Description:   j.Description,
			MaxMember:     j.MaxMembers,
			CurrentMember: 1,
		}); err != nil {
			return err
		}
		return q.CreateGroupLeaderRole(ctx, database.CreateGroupLeaderRoleParams{
			GroupID:  j.GroupID,
			MemberID: j.CreatorID,
			Role:     RoleLeader,
		})
	})
}

func (r *groupRepo) NameTaken(ctx context.Context, name string) (bool, error) {
	_, err := r.q.SearchInfoByName(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *groupRepo) Snapshot(ctx context.Context, groupID uuid.UUID) (GroupSnapshot, error) {
	g, err := r.q.GetGroupInfoByID(ctx, groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return GroupSnapshot{}, ErrNotFoundGroup
	}
	if err != nil {
		return GroupSnapshot{}, err
	}
	members, err := r.q.GetMemberListByID(ctx, groupID)
	if err != nil {
		return GroupSnapshot{}, err
	}
	leader, err := r.q.GetGroupLeader(ctx, groupID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return GroupSnapshot{}, err
	}
	return GroupSnapshot{Name: g.Name, MaxMem: g.MaxMember, LeaderID: leader, Members: members}, nil
}

func (r *groupRepo) AddMember(ctx context.Context, groupID, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		if err := q.AddMember(ctx, database.AddMemberParams{GroupID: groupID, MemberID: userID}); err != nil {
			return err
		}
		return q.UpdateGroupCurrentMemberByID(ctx, groupID)
	})
}

func (r *groupRepo) RemoveMember(ctx context.Context, groupID, userID uuid.UUID) (bool, error) {
	var deleted bool
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		n, err := q.DeleteMemFromGroup(ctx, database.DeleteMemFromGroupParams{GroupID: groupID, MemberID: userID})
		if err != nil {
			return err
		}
		deleted = n > 0
		return q.UpdateGroupCurrentMemberByID(ctx, groupID)
	})
	return deleted, err
}

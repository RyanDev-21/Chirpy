package groups

import (
	"context"
	"errors"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GroupSnapshot is what the cache keeps about one group.
type GroupSnapshot struct {
	Name     string      `json:"name"`
	MaxMem   int16       `json:"max"`
	LeaderID uuid.UUID   `json:"leader"`
	Members  []uuid.UUID `json:"members"`
}

type GroupRepo interface {
	// Create inserts the group and its leader; ErrDuplicateName if the name
	// is taken (case-insensitive).
	Create(ctx context.Context, groupID, creatorID uuid.UUID, req createGroupRequest) error
	Snapshot(ctx context.Context, groupID uuid.UUID) (GroupSnapshot, error)
	// Join and Leave lock the group row, so concurrent changes to the same
	// group are applied one at a time and capacity cannot be exceeded.
	Join(ctx context.Context, groupID, userID uuid.UUID) error
	Leave(ctx context.Context, groupID, userID uuid.UUID) error
}

type groupRepo struct {
	pool *pgxpool.Pool
	q    *database.Queries
}

func NewGroupRepo(pool *pgxpool.Pool) GroupRepo {
	return &groupRepo{pool: pool, q: database.New(pool)}
}

func isCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

func (r *groupRepo) Create(ctx context.Context, groupID, creatorID uuid.UUID, req createGroupRequest) error {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		if err := q.CreateGroup(ctx, database.CreateGroupParams{
			ID: groupID, Name: req.GroupName, Description: req.Description, MaxMember: req.MaxMems,
		}); err != nil {
			return err
		}
		return q.CreateGroupLeaderRole(ctx, database.CreateGroupLeaderRoleParams{GroupID: groupID, MemberID: creatorID})
	})
	if isCode(err, "23505") {
		return ErrDuplicateName
	}
	return err
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

// withLockedGroup runs fn in a transaction holding the group's row lock.
func (r *groupRepo) withLockedGroup(ctx context.Context, groupID uuid.UUID, fn func(q *database.Queries, g database.LockGroupRow) error) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		g, err := q.LockGroup(ctx, groupID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFoundGroup
		}
		if err != nil {
			return err
		}
		return fn(q, g)
	})
}

func (r *groupRepo) Join(ctx context.Context, groupID, userID uuid.UUID) error {
	return r.withLockedGroup(ctx, groupID, func(q *database.Queries, g database.LockGroupRow) error {
		_, err := q.GetMemberRole(ctx, database.GetMemberRoleParams{GroupID: groupID, MemberID: userID})
		if err == nil {
			return ErrAlreadyMember
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if g.CurrentMember >= g.MaxMember {
			return ErrGroupFull
		}
		if err := q.AddMember(ctx, database.AddMemberParams{GroupID: groupID, MemberID: userID}); err != nil {
			return err
		}
		return q.SetGroupMemberCount(ctx, database.SetGroupMemberCountParams{ID: groupID, CurrentMember: g.CurrentMember + 1})
	})
}

func (r *groupRepo) Leave(ctx context.Context, groupID, userID uuid.UUID) error {
	return r.withLockedGroup(ctx, groupID, func(q *database.Queries, g database.LockGroupRow) error {
		role, err := q.GetMemberRole(ctx, database.GetMemberRoleParams{GroupID: groupID, MemberID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotMember
		}
		if err != nil {
			return err
		}
		if role == RoleLeader {
			return ErrLeaderCannotLeave
		}
		if err := q.DeleteMemFromGroup(ctx, database.DeleteMemFromGroupParams{GroupID: groupID, MemberID: userID}); err != nil {
			return err
		}
		return q.SetGroupMemberCount(ctx, database.SetGroupMemberCountParams{ID: groupID, CurrentMember: max(g.CurrentMember-1, 0)})
	})
}

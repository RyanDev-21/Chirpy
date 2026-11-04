package groups

import (
	"context"
	"errors"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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
	ListForUser(ctx context.Context, userID uuid.UUID) ([]GroupSummary, error)
	// ByInviteCode finds the group for a normalized code (ErrNotFoundGroup).
	ByInviteCode(ctx context.Context, code string) (GroupPreview, error)
	InviteCode(ctx context.Context, groupID uuid.UUID) (string, error)
	// RegenerateInviteCode replaces the code, invalidating the old one.
	RegenerateInviteCode(ctx context.Context, groupID uuid.UUID) (string, error)
	Detail(ctx context.Context, groupID, viewer uuid.UUID) (GroupDetail, error)
}

type groupRepo struct {
	pool *pgxpool.Pool
	q    *database.Queries
}

func NewGroupRepo(pool *pgxpool.Pool) GroupRepo {
	return &groupRepo{pool: pool, q: database.New(pool)}
}

func constraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName
	}
	return ""
}

const inviteCodeConstraint = "chat_groups_invite_code_key"

func (r *groupRepo) Create(ctx context.Context, groupID, creatorID uuid.UUID, req createGroupRequest) error {
	// an invite code collision is ~1 in a trillion; retry a few times anyway
	for range 3 {
		code, err := NewInviteCode()
		if err != nil {
			return err
		}
		err = pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
			q := r.q.WithTx(tx)
			if err := q.CreateGroup(ctx, database.CreateGroupParams{
				ID: groupID, Name: req.GroupName, Description: req.Description, MaxMember: req.MaxMems, InviteCode: code,
			}); err != nil {
				return err
			}
			return q.CreateGroupLeaderRole(ctx, database.CreateGroupLeaderRoleParams{GroupID: groupID, MemberID: creatorID, JoinedAt: nowUTC()})
		})
		switch c := constraint(err); {
		case c == inviteCodeConstraint:
			continue
		case c != "":
			return ErrDuplicateName
		}
		return err
	}
	return errors.New("could not allocate a unique invite code")
}

func (r *groupRepo) ByInviteCode(ctx context.Context, code string) (GroupPreview, error) {
	g, err := r.q.GetGroupByInviteCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return GroupPreview{}, ErrNotFoundGroup
	}
	if err != nil {
		return GroupPreview{}, err
	}
	return GroupPreview{ID: g.ID, Name: g.Name, Description: g.Description, MaxMembers: g.MaxMember, MemberCount: g.CurrentMember}, nil
}

func (r *groupRepo) InviteCode(ctx context.Context, groupID uuid.UUID) (string, error) {
	code, err := r.q.GetInviteCode(ctx, groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFoundGroup
	}
	return code, err
}

func (r *groupRepo) RegenerateInviteCode(ctx context.Context, groupID uuid.UUID) (string, error) {
	for range 3 {
		code, err := NewInviteCode()
		if err != nil {
			return "", err
		}
		err = r.q.SetInviteCode(ctx, database.SetInviteCodeParams{ID: groupID, InviteCode: code})
		if constraint(err) == inviteCodeConstraint {
			continue
		}
		return code, err
	}
	return "", errors.New("could not allocate a unique invite code")
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
		if err := q.AddMember(ctx, database.AddMemberParams{GroupID: groupID, MemberID: userID, JoinedAt: nowUTC()}); err != nil {
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

func (r *groupRepo) ListForUser(ctx context.Context, userID uuid.UUID) ([]GroupSummary, error) {
	rows, err := r.q.ListGroupsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]GroupSummary, 0, len(rows))
	for _, g := range rows {
		out = append(out, GroupSummary{ID: g.ID, Name: g.Name, Description: g.Description, MaxMembers: g.MaxMember, Members: g.CurrentMember, Role: g.Role})
	}
	return out, nil
}

func (r *groupRepo) Detail(ctx context.Context, groupID, viewer uuid.UUID) (GroupDetail, error) {
	g, err := r.q.GetGroupInfoByID(ctx, groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return GroupDetail{}, ErrNotFoundGroup
	}
	if err != nil {
		return GroupDetail{}, err
	}
	d := GroupDetail{ID: g.ID, Name: g.Name, Description: g.Description, MaxMembers: g.MaxMember, MemberCount: g.CurrentMember}
	rows, err := r.q.ListGroupMembers(ctx, groupID)
	if err != nil {
		return GroupDetail{}, err
	}
	members := make([]GroupMember, 0, len(rows))
	for _, m := range rows {
		members = append(members, GroupMember{UserID: m.MemberID, Name: m.Name, Role: m.Role})
		if m.MemberID == viewer {
			d.IsMember = true
		}
	}
	if d.IsMember {
		d.Members = members // the member list is only shown to members
	}
	return d, nil
}

// nowUTC is the server clock, which also stamps every message: comparing
// join times with message times must not mix in the database's clock.
func nowUTC() pgtype.Timestamp { return pgtype.Timestamp{Time: time.Now().UTC(), Valid: true} }

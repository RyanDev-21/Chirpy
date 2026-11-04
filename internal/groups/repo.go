package groups

import (
	"context"
	"errors"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GroupSnapshot is what the cache keeps about one group.
type GroupSnapshot struct {
	Name    string      `json:"name"`
	MaxMem  int16       `json:"max"`
	AdminID uuid.UUID   `json:"admin"`
	Public  bool        `json:"public"`
	Members []uuid.UUID `json:"members"`
	// Mods: each moderator's permissions.
	Mods map[uuid.UUID][]string `json:"mods,omitempty"`
}

// Role is a member's role and (for moderators) permissions; "" if not a
// member.
func (g GroupSnapshot) Role(user uuid.UUID) (string, []string) {
	switch {
	case user == g.AdminID:
		return RoleAdmin, nil
	case g.Mods[user] != nil:
		return RoleModerator, g.Mods[user]
	}
	for _, m := range g.Members {
		if m == user {
			return RoleMember, nil
		}
	}
	return "", nil
}

// Leaving is the result of a member leaving.
type Leaving struct {
	NewAdmin uuid.UUID   // set when the admin left and handed over
	Deleted  bool        // the admin was the last one: the group is gone
	Members  []uuid.UUID // who was in a deleted group
}

type GroupRepo interface {
	// Create inserts the group and its admin.
	Create(ctx context.Context, groupID, creatorID uuid.UUID, req createGroupRequest) error
	Snapshot(ctx context.Context, groupID uuid.UUID) (GroupSnapshot, error)
	// Join and Leave lock the group row, so concurrent changes to the same
	// group are applied one at a time and capacity cannot be exceeded.
	Join(ctx context.Context, groupID, userID uuid.UUID) error
	// Leave: an admin hands over to successor (or, if Nil, the longest-
	// serving moderator, then member); the last one out deletes the group.
	Leave(ctx context.Context, groupID, userID, successor uuid.UUID) (Leaving, error)
	ListForUser(ctx context.Context, userID uuid.UUID) ([]GroupSummary, error)
	// ByInviteCode finds the group for a normalized code (ErrNotFoundGroup).
	ByInviteCode(ctx context.Context, code string) (GroupPreview, error)
	InviteCode(ctx context.Context, groupID uuid.UUID) (string, error)
	// RegenerateInviteCode replaces the code, invalidating the old one.
	RegenerateInviteCode(ctx context.Context, groupID uuid.UUID) (string, error)
	Detail(ctx context.Context, groupID, viewer uuid.UUID) (GroupDetail, error)
	// Search lists public groups whose name contains q.
	Search(ctx context.Context, viewer uuid.UUID, q string) ([]GroupPreview, error)

	// Managing the group. Each locks the group row and checks the actor's
	// role (ErrNotFoundGroup for non-members, ErrNoPermission).
	Kick(ctx context.Context, groupID, actor, target uuid.UUID) error
	// Update changes name, description and/or visibility (nil keeps a
	// field); visibility is admin only.
	Update(ctx context.Context, groupID, actor uuid.UUID, name, desc, visibility *string) (GroupPreview, error)
	// TransferAdmin makes target the admin; the old admin becomes a member.
	TransferAdmin(ctx context.Context, groupID, admin, target uuid.UUID) error
	// SetRole (admin only) makes target a moderator with perms, or a member.
	SetRole(ctx context.Context, groupID, admin, target uuid.UUID, role string, perms []string) error
	// Delete removes the group with its messages; returns who was in it.
	Delete(ctx context.Context, groupID, admin uuid.UUID) ([]uuid.UUID, error)
	// AddMember adds target directly (ErrAlreadyMember, ErrGroupFull).
	AddMember(ctx context.Context, groupID, actor, target uuid.UUID) error
	SetAvatar(ctx context.Context, groupID, actor uuid.UUID, img []byte, at time.Time) error
	DeleteAvatar(ctx context.Context, groupID, actor uuid.UUID) error
	// Avatar is ErrNoAvatar without a picture or for a non-member of a
	// private group.
	Avatar(ctx context.Context, groupID, viewer uuid.UUID) ([]byte, time.Time, error)
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
				ID: groupID, Name: req.GroupName, Description: req.Description, MaxMember: req.MaxMems, InviteCode: code, Visibility: req.Visibility,
			}); err != nil {
				return err
			}
			return q.CreateGroupLeaderRole(ctx, database.CreateGroupLeaderRoleParams{GroupID: groupID, MemberID: creatorID, JoinedAt: nowUTC()})
		})
		if constraint(err) == inviteCodeConstraint {
			continue
		}
		return err
	}
	return errors.New("could not allocate a unique invite code")
}

func preview(id uuid.UUID, name, desc string, max, n int16, vis string, avatarAt pgtype.Timestamp) GroupPreview {
	p := GroupPreview{ID: id, Name: name, Description: desc, MaxMembers: max, MemberCount: n, Visibility: vis}
	if vis == Public {
		p.AvatarURL = AvatarURL(id, avatarAt)
	}
	return p
}

func (r *groupRepo) ByInviteCode(ctx context.Context, code string) (GroupPreview, error) {
	g, err := r.q.GetGroupByInviteCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return GroupPreview{}, ErrNotFoundGroup
	}
	if err != nil {
		return GroupPreview{}, err
	}
	return preview(g.ID, g.Name, g.Description, g.MaxMember, g.CurrentMember, g.Visibility, pgtype.Timestamp{}), nil
}

func (r *groupRepo) Search(ctx context.Context, viewer uuid.UUID, q string) ([]GroupPreview, error) {
	rows, err := r.q.SearchPublicGroups(ctx, database.SearchPublicGroupsParams{Viewer: viewer, Q: q})
	if err != nil {
		return nil, err
	}
	out := make([]GroupPreview, 0, len(rows))
	for _, g := range rows {
		p := preview(g.ID, g.Name, g.Description, g.MaxMember, g.CurrentMember, Public, g.AvatarAt)
		member := g.IsMember
		p.IsMember = &member
		out = append(out, p)
	}
	return out, nil
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
	admin, err := r.q.GetGroupLeader(ctx, groupID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return GroupSnapshot{}, err
	}
	mods, err := r.q.ModeratorsOf(ctx, groupID)
	if err != nil {
		return GroupSnapshot{}, err
	}
	snap := GroupSnapshot{Name: g.Name, MaxMem: g.MaxMember, AdminID: admin, Public: g.Visibility == Public, Members: members}
	if len(mods) > 0 {
		snap.Mods = make(map[uuid.UUID][]string, len(mods))
		for _, m := range mods {
			snap.Mods[m.MemberID] = append([]string{}, m.Permissions...)
		}
	}
	return snap, nil
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

// member is the role and permissions of userID, inside a transaction.
func member(ctx context.Context, q *database.Queries, groupID, userID uuid.UUID) (database.GetMemberRow, bool, error) {
	m, err := q.GetMember(ctx, database.GetMemberParams{GroupID: groupID, MemberID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return m, false, nil
	}
	return m, err == nil, err
}

func (r *groupRepo) addLocked(ctx context.Context, q *database.Queries, g database.LockGroupRow, groupID, userID uuid.UUID) error {
	if _, in, err := member(ctx, q, groupID, userID); err != nil || in {
		if in {
			return ErrAlreadyMember
		}
		return err
	}
	if g.CurrentMember >= g.MaxMember {
		return ErrGroupFull
	}
	if err := q.AddMember(ctx, database.AddMemberParams{GroupID: groupID, MemberID: userID, JoinedAt: nowUTC()}); err != nil {
		return err
	}
	return q.SetGroupMemberCount(ctx, database.SetGroupMemberCountParams{ID: groupID, CurrentMember: g.CurrentMember + 1})
}

func (r *groupRepo) Join(ctx context.Context, groupID, userID uuid.UUID) error {
	return r.withLockedGroup(ctx, groupID, func(q *database.Queries, g database.LockGroupRow) error {
		return r.addLocked(ctx, q, g, groupID, userID)
	})
}

func (r *groupRepo) Leave(ctx context.Context, groupID, userID, successor uuid.UUID) (Leaving, error) {
	var out Leaving
	err := r.withLockedGroup(ctx, groupID, func(q *database.Queries, g database.LockGroupRow) error {
		m, in, err := member(ctx, q, groupID, userID)
		if err != nil {
			return err
		}
		if !in {
			return ErrNotMember
		}
		if m.Role == RoleAdmin {
			next := successor
			if next == uuid.Nil {
				next, err = q.NextAdmin(ctx, database.NextAdminParams{GroupID: groupID, Leaving: userID})
				if errors.Is(err, pgx.ErrNoRows) { // nobody left: the group goes
					out.Deleted, out.Members = true, []uuid.UUID{userID}
					return q.DeleteGroup(ctx, groupID)
				}
				if err != nil {
					return err
				}
			} else if _, ok, err := member(ctx, q, groupID, next); err != nil || !ok || next == userID {
				if err != nil {
					return err
				}
				return ErrNotMember // the successor must be another member
			}
			if err := q.SetMemberRole(ctx, database.SetMemberRoleParams{GroupID: groupID, MemberID: next, Role: RoleAdmin, Permissions: []string{}}); err != nil {
				return err
			}
			out.NewAdmin = next
		}
		if err := q.DeleteMemFromGroup(ctx, database.DeleteMemFromGroupParams{GroupID: groupID, MemberID: userID}); err != nil {
			return err
		}
		return q.SetGroupMemberCount(ctx, database.SetGroupMemberCountParams{ID: groupID, CurrentMember: max(g.CurrentMember-1, 0)})
	})
	return out, err
}

func (r *groupRepo) ListForUser(ctx context.Context, userID uuid.UUID) ([]GroupSummary, error) {
	rows, err := r.q.ListGroupsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]GroupSummary, 0, len(rows))
	for _, g := range rows {
		out = append(out, GroupSummary{ID: g.ID, Name: g.Name, Description: g.Description, MaxMembers: g.MaxMember, Members: g.CurrentMember,
			Visibility: g.Visibility, Role: g.Role, Permissions: nonNil(g.Permissions), AvatarURL: AvatarURL(g.ID, g.AvatarAt)})
	}
	return out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (r *groupRepo) Detail(ctx context.Context, groupID, viewer uuid.UUID) (GroupDetail, error) {
	g, err := r.q.GetGroupInfoByID(ctx, groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return GroupDetail{}, ErrNotFoundGroup
	}
	if err != nil {
		return GroupDetail{}, err
	}
	d := GroupDetail{ID: g.ID, Name: g.Name, Description: g.Description, MaxMembers: g.MaxMember, MemberCount: g.CurrentMember, Visibility: g.Visibility}
	rows, err := r.q.ListGroupMembers(ctx, groupID)
	if err != nil {
		return GroupDetail{}, err
	}
	members := make([]GroupMember, 0, len(rows))
	for _, m := range rows {
		members = append(members, GroupMember{UserID: m.MemberID, Name: m.Name, DisplayName: users.ShownName(m.Name, m.DisplayName),
			Role: m.Role, Permissions: nonNil(m.Permissions), AvatarURL: users.AvatarURLFrom(m.MemberID, m.AvatarAt)})
		if m.MemberID == viewer {
			d.IsMember = true
		}
	}
	if d.IsMember {
		d.Members = members // the member list is only shown to members
	}
	if d.IsMember || g.Visibility == Public {
		d.AvatarURL = AvatarURL(g.ID, g.AvatarAt)
	}
	return d, nil
}

// as runs fn in the group's locked transaction if actor may do perm (""
// = admin only); fn gets the actor's role.
func (r *groupRepo) as(ctx context.Context, groupID, actor uuid.UUID, perm string, fn func(q *database.Queries, g database.LockGroupRow, role string) error) error {
	return r.withLockedGroup(ctx, groupID, func(q *database.Queries, g database.LockGroupRow) error {
		m, in, err := member(ctx, q, groupID, actor)
		if err != nil {
			return err
		}
		if !in {
			return ErrNotFoundGroup // private: don't confirm the group exists
		}
		if !allowed(m.Role, m.Permissions, perm) {
			return ErrNoPermission
		}
		return fn(q, g, m.Role)
	})
}

func (r *groupRepo) Kick(ctx context.Context, groupID, actor, target uuid.UUID) error {
	return r.as(ctx, groupID, actor, PermRemoveMembers, func(q *database.Queries, g database.LockGroupRow, role string) error {
		if target == actor {
			return ErrKickSelf
		}
		t, in, err := member(ctx, q, groupID, target)
		if err != nil {
			return err
		}
		if !in {
			return ErrNotMember
		}
		switch {
		case t.Role == RoleAdmin:
			return ErrCantRemoveAdmin
		case t.Role == RoleModerator && role != RoleAdmin:
			return ErrNoPermission // only the admin removes moderators
		}
		if err := q.DeleteMemFromGroup(ctx, database.DeleteMemFromGroupParams{GroupID: groupID, MemberID: target}); err != nil {
			return err
		}
		return q.SetGroupMemberCount(ctx, database.SetGroupMemberCountParams{ID: groupID, CurrentMember: max(g.CurrentMember-1, 0)})
	})
}

func optText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func (r *groupRepo) Update(ctx context.Context, groupID, actor uuid.UUID, name, desc, visibility *string) (GroupPreview, error) {
	var out GroupPreview
	err := r.as(ctx, groupID, actor, PermEditInfo, func(q *database.Queries, g database.LockGroupRow, role string) error {
		if visibility != nil && role != RoleAdmin {
			return ErrNoPermission
		}
		row, err := q.UpdateGroupInfo(ctx, database.UpdateGroupInfoParams{ID: groupID, Name: optText(name), Description: optText(desc), Visibility: optText(visibility)})
		if err != nil {
			return err
		}
		out = GroupPreview{ID: groupID, Name: row.Name, Description: row.Description, MaxMembers: g.MaxMember, MemberCount: g.CurrentMember, Visibility: row.Visibility}
		return nil
	})
	return out, err
}

func (r *groupRepo) TransferAdmin(ctx context.Context, groupID, admin, target uuid.UUID) error {
	return r.as(ctx, groupID, admin, "", func(q *database.Queries, _ database.LockGroupRow, _ string) error {
		if target == admin {
			return ErrAlreadyAdmin
		}
		if _, in, err := member(ctx, q, groupID, target); err != nil || !in {
			if err != nil {
				return err
			}
			return ErrNotMember
		}
		if err := q.SetMemberRole(ctx, database.SetMemberRoleParams{GroupID: groupID, MemberID: admin, Role: RoleMember, Permissions: []string{}}); err != nil {
			return err
		}
		return q.SetMemberRole(ctx, database.SetMemberRoleParams{GroupID: groupID, MemberID: target, Role: RoleAdmin, Permissions: []string{}})
	})
}

func (r *groupRepo) SetRole(ctx context.Context, groupID, admin, target uuid.UUID, role string, perms []string) error {
	return r.as(ctx, groupID, admin, "", func(q *database.Queries, _ database.LockGroupRow, _ string) error {
		if target == admin {
			return ErrSelfRole
		}
		if _, in, err := member(ctx, q, groupID, target); err != nil || !in {
			if err != nil {
				return err
			}
			return ErrNotMember
		}
		if role == RoleMember {
			perms = []string{}
		}
		return q.SetMemberRole(ctx, database.SetMemberRoleParams{GroupID: groupID, MemberID: target, Role: role, Permissions: perms})
	})
}

func (r *groupRepo) Delete(ctx context.Context, groupID, admin uuid.UUID) ([]uuid.UUID, error) {
	var members []uuid.UUID
	err := r.as(ctx, groupID, admin, "", func(q *database.Queries, _ database.LockGroupRow, _ string) error {
		var err error
		if members, err = q.GetMemberListByID(ctx, groupID); err != nil {
			return err
		}
		return q.DeleteGroup(ctx, groupID)
	})
	return members, err
}

// AddMember: the actor adds target (the caller checks friendship).
func (r *groupRepo) AddMember(ctx context.Context, groupID, actor, target uuid.UUID) error {
	return r.as(ctx, groupID, actor, PermAddMembers, func(q *database.Queries, g database.LockGroupRow, _ string) error {
		return r.addLocked(ctx, q, g, groupID, target)
	})
}

func (r *groupRepo) SetAvatar(ctx context.Context, groupID, actor uuid.UUID, img []byte, at time.Time) error {
	return r.as(ctx, groupID, actor, PermEditInfo, func(q *database.Queries, _ database.LockGroupRow, _ string) error {
		t := pgtype.Timestamp{Time: at, Valid: true}
		if err := q.UpsertGroupAvatar(ctx, database.UpsertGroupAvatarParams{GroupID: groupID, Image: img, At: t}); err != nil {
			return err
		}
		return q.SetGroupAvatarAt(ctx, database.SetGroupAvatarAtParams{ID: groupID, At: t})
	})
}

func (r *groupRepo) DeleteAvatar(ctx context.Context, groupID, actor uuid.UUID) error {
	return r.as(ctx, groupID, actor, PermEditInfo, func(q *database.Queries, _ database.LockGroupRow, _ string) error {
		if err := q.DeleteGroupAvatar(ctx, groupID); err != nil {
			return err
		}
		return q.SetGroupAvatarAt(ctx, database.SetGroupAvatarAtParams{ID: groupID})
	})
}

func (r *groupRepo) Avatar(ctx context.Context, groupID, viewer uuid.UUID) ([]byte, time.Time, error) {
	a, err := r.q.GroupAvatarForMember(ctx, database.GroupAvatarForMemberParams{GroupID: groupID, Viewer: viewer})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, ErrNoAvatar
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	return a.Image, a.UpdatedAt.Time.UTC(), nil
}

// nowUTC is the server clock, which also stamps every message: comparing
// join times with message times must not mix in the database's clock.
func nowUTC() pgtype.Timestamp { return pgtype.Timestamp{Time: time.Now().UTC(), Valid: true} }

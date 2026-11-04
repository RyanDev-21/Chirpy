package groups

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/push"
	"RyanDev-21.com/Chirpy/internal/users"
	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type GroupService interface {
	CreateGroup(ctx context.Context, creatorID uuid.UUID, req createGroupRequest) (*GroupInfo, error)
	JoinGroup(ctx context.Context, groupID, userID uuid.UUID) error
	// JoinPublic joins a public group without a code (ErrNotFoundGroup for
	// private ones).
	JoinPublic(ctx context.Context, groupID, userID uuid.UUID) error
	// LeaveGroup: an admin may name their successor (uuid.Nil: the
	// longest-serving moderator, then member).
	LeaveGroup(ctx context.Context, groupID, userID, successor uuid.UUID) error
	MyGroups(ctx context.Context, userID uuid.UUID) ([]GroupSummary, error)
	GroupDetail(ctx context.Context, groupID, viewer uuid.UUID) (GroupDetail, error)
	// Search finds public groups by name.
	Search(ctx context.Context, viewer uuid.UUID, q string) ([]GroupPreview, error)
	// PreviewInvite shows what a code leads to before joining.
	PreviewInvite(ctx context.Context, code string) (GroupPreview, error)
	JoinByInvite(ctx context.Context, userID uuid.UUID, code string) (uuid.UUID, error)
	// InviteCode is visible to members; RegenerateInvite needs manage_invite.
	InviteCode(ctx context.Context, groupID, viewer uuid.UUID) (string, error)
	RegenerateInvite(ctx context.Context, groupID, userID uuid.UUID) (string, error)
	// IsMember and Members are used by chat for authorization and fan-out.
	IsMember(ctx context.Context, groupID, userID uuid.UUID) (bool, error)
	Members(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)
	// CanDeleteMessage: may actor delete sender's message in the group?
	// The admin may delete anyone's; a moderator with delete_messages only
	// ordinary members'.
	CanDeleteMessage(ctx context.Context, groupID, actor, sender uuid.UUID) (bool, error)

	// Managing the group (admin, or a moderator with the permission).
	Kick(ctx context.Context, groupID, actor, target uuid.UUID) error
	Update(ctx context.Context, groupID, actor uuid.UUID, req updateGroupRequest) (GroupPreview, error)
	TransferAdmin(ctx context.Context, groupID, admin, target uuid.UUID) error
	SetRole(ctx context.Context, groupID, admin, target uuid.UUID, req roleRequest) error
	Delete(ctx context.Context, groupID, admin uuid.UUID) error
	// OnDelete registers cleanup to run after a group is deleted.
	OnDelete(fn func(ctx context.Context, groupID uuid.UUID))
	// GroupName is the group's display name (cached).
	GroupName(ctx context.Context, groupID uuid.UUID) (string, error)

	// AddFriend: the admin or a moderator with add_members adds one of
	// their friends directly.
	AddFriend(ctx context.Context, groupID, actor, target uuid.UUID) error
	// SetAvatar (edit_info) stores raw as a 256x256 JPEG.
	SetAvatar(ctx context.Context, groupID, actor uuid.UUID, raw []byte) (*string, error)
	DeleteAvatar(ctx context.Context, groupID, actor uuid.UUID) error
	Avatar(ctx context.Context, groupID, viewer uuid.UUID) ([]byte, time.Time, error)
	// SetFriends and SetNotifier must be called before AddFriend is used.
	SetFriends(f FriendChecker)
	SetNotifier(n push.Notifier)
}

// FriendChecker is implemented by friends.Service.
type FriendChecker interface {
	AreFriends(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// Pusher delivers realtime events (implemented by *ws.Hub).
type Pusher interface {
	WriteToUsers(targets []uuid.UUID, ev ws.Event) error
}

type groupService struct {
	friends  FriendChecker
	notify   push.Notifier // nil: no push notifications
	repo     GroupRepo
	store    Store
	push     Pusher
	onDelete []func(ctx context.Context, groupID uuid.UUID)
	logger   *slog.Logger
}

func NewGroupService(repo GroupRepo, store Store, push Pusher, logger *slog.Logger) GroupService {
	return &groupService{repo: repo, store: store, push: push, logger: logger}
}

func validVisibility(v string) bool { return v == Private || v == Public }

func validateCreate(req *createGroupRequest) error {
	req.GroupName = strings.TrimSpace(req.GroupName)
	if req.Visibility == "" {
		req.Visibility = Private
	}
	n := utf8.RuneCountInString(req.GroupName)
	if n == 0 || n > maxGroupNameLen || utf8.RuneCountInString(req.Description) > maxDescLen || !validVisibility(req.Visibility) {
		return ErrInvalidGroupReq
	}
	if req.MaxMems < 2 || req.MaxMems > maxGroupSize {
		return ErrInvalidGroupReq
	}
	return nil
}

func (s *groupService) CreateGroup(ctx context.Context, creatorID uuid.UUID, req createGroupRequest) (*GroupInfo, error) {
	if err := validateCreate(&req); err != nil {
		return nil, err
	}
	groupID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, groupID, creatorID, req); err != nil {
		return nil, err
	}
	return &GroupInfo{ChatID: groupID}, nil
}

func (s *groupService) JoinGroup(ctx context.Context, groupID, userID uuid.UUID) error {
	if err := s.repo.Join(ctx, groupID, userID); err != nil {
		return err
	}
	s.invalidate(ctx, groupID)
	s.pushMembers(ctx, groupID, userID, "joined", userID)
	return nil
}

func (s *groupService) JoinPublic(ctx context.Context, groupID, userID uuid.UUID) error {
	snap, err := s.snapshot(ctx, groupID)
	if err != nil {
		return err
	}
	if !snap.Public {
		return ErrNotFoundGroup // private groups need a code or an invite
	}
	return s.JoinGroup(ctx, groupID, userID)
}

func (s *groupService) LeaveGroup(ctx context.Context, groupID, userID, successor uuid.UUID) error {
	left, err := s.repo.Leave(ctx, groupID, userID, successor)
	if err != nil {
		return err
	}
	s.invalidate(ctx, groupID)
	if left.Deleted {
		for _, fn := range s.onDelete {
			fn(ctx, groupID)
		}
		return nil
	}
	if left.NewAdmin != uuid.Nil {
		s.pushMembers(ctx, groupID, userID, "admin", left.NewAdmin)
	}
	s.pushMembers(ctx, groupID, userID, "members", userID)
	return nil
}

func (s *groupService) MyGroups(ctx context.Context, userID uuid.UUID) ([]GroupSummary, error) {
	return s.repo.ListForUser(ctx, userID)
}

// GroupDetail: members see everything; anyone sees a public group's
// details without the member list; a private group is ErrNotFoundGroup
// for non-members, who don't learn it exists.
func (s *groupService) GroupDetail(ctx context.Context, groupID, viewer uuid.UUID) (GroupDetail, error) {
	d, err := s.repo.Detail(ctx, groupID, viewer)
	if err != nil {
		return GroupDetail{}, err
	}
	if !d.IsMember && d.Visibility != Public {
		return GroupDetail{}, ErrNotFoundGroup
	}
	return d, nil
}

// escapeLike makes q a literal LIKE pattern (with ESCAPE '\').
func escapeLike(q string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
}

func (s *groupService) Search(ctx context.Context, viewer uuid.UUID, q string) ([]GroupPreview, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if n := utf8.RuneCountInString(q); n < 2 || n > maxGroupNameLen {
		return nil, ErrBadSearch
	}
	return s.repo.Search(ctx, viewer, escapeLike(q))
}

func (s *groupService) PreviewInvite(ctx context.Context, raw string) (GroupPreview, error) {
	code, err := NormalizeInviteCode(raw)
	if err != nil {
		return GroupPreview{}, err
	}
	return s.repo.ByInviteCode(ctx, code)
}

func (s *groupService) JoinByInvite(ctx context.Context, userID uuid.UUID, raw string) (uuid.UUID, error) {
	g, err := s.PreviewInvite(ctx, raw)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.JoinGroup(ctx, g.ID, userID); err != nil {
		return uuid.Nil, err
	}
	return g.ID, nil
}

func (s *groupService) InviteCode(ctx context.Context, groupID, viewer uuid.UUID) (string, error) {
	ok, err := s.IsMember(ctx, groupID, viewer)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotFoundGroup
	}
	code, err := s.repo.InviteCode(ctx, groupID)
	return FormatInviteCode(code), err
}

func (s *groupService) RegenerateInvite(ctx context.Context, groupID, userID uuid.UUID) (string, error) {
	if err := s.check(ctx, groupID, userID, PermManageInvite); err != nil {
		return "", err
	}
	code, err := s.repo.RegenerateInviteCode(ctx, groupID)
	return FormatInviteCode(code), err
}

// check: may user do perm ("" = admin only)? Uses the cached snapshot,
// which every role change invalidates.
func (s *groupService) check(ctx context.Context, groupID, user uuid.UUID, perm string) error {
	snap, err := s.snapshot(ctx, groupID)
	if err != nil {
		return err
	}
	role, perms := snap.Role(user)
	if role == "" {
		return ErrNotFoundGroup
	}
	if !allowed(role, perms, perm) {
		return ErrNoPermission
	}
	return nil
}

func (s *groupService) snapshot(ctx context.Context, groupID uuid.UUID) (GroupSnapshot, error) {
	return s.store.Load(ctx, groupID.String(), func(ctx context.Context) (GroupSnapshot, error) {
		return s.repo.Snapshot(ctx, groupID)
	}, func(err error) { s.logger.WarnContext(ctx, "group cache error", "error", err) })
}

func (s *groupService) IsMember(ctx context.Context, groupID, userID uuid.UUID) (bool, error) {
	snap, err := s.snapshot(ctx, groupID)
	if err != nil {
		return false, err
	}
	return slices.Contains(snap.Members, userID), nil
}

func (s *groupService) Members(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	snap, err := s.snapshot(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return slices.Clone(snap.Members), nil
}

func (s *groupService) CanDeleteMessage(ctx context.Context, groupID, actor, sender uuid.UUID) (bool, error) {
	snap, err := s.snapshot(ctx, groupID)
	if err != nil {
		return false, err
	}
	role, perms := snap.Role(actor)
	switch {
	case role == RoleAdmin:
		return true, nil
	case !allowed(role, perms, PermDeleteMessages):
		return false, nil
	}
	// moderators can't delete the admin's or other moderators' messages
	senderRole, _ := snap.Role(sender)
	return senderRole != RoleAdmin && senderRole != RoleModerator, nil
}

func (s *groupService) invalidate(ctx context.Context, groupID uuid.UUID) {
	if err := s.store.Invalidate(ctx, groupID.String()); err != nil {
		s.logger.ErrorContext(ctx, "failed to invalidate group cache", "group", groupID, "error", err)
	}
}

// --- managing the group ---

func (s *groupService) Kick(ctx context.Context, groupID, actor, target uuid.UUID) error {
	if err := s.repo.Kick(ctx, groupID, actor, target); err != nil {
		return err
	}
	s.invalidate(ctx, groupID)
	s.pushTo(ctx, []uuid.UUID{target}, ws.EventGroupGone, ws.OutGroupRemoved{GroupID: groupID.String(), Reason: "kicked"})
	s.pushMembers(ctx, groupID, actor, "members", target)
	return nil
}

func (s *groupService) Update(ctx context.Context, groupID, actor uuid.UUID, req updateGroupRequest) (GroupPreview, error) {
	if req.GroupName == nil && req.Description == nil && req.Visibility == nil {
		return GroupPreview{}, ErrNothingToUpdate
	}
	if req.GroupName != nil {
		name := strings.TrimSpace(*req.GroupName)
		if n := utf8.RuneCountInString(name); n == 0 || n > maxGroupNameLen {
			return GroupPreview{}, ErrInvalidGroupInfo
		}
		req.GroupName = &name
	}
	if req.Description != nil && utf8.RuneCountInString(*req.Description) > maxDescLen {
		return GroupPreview{}, ErrInvalidGroupInfo
	}
	if req.Visibility != nil && !validVisibility(*req.Visibility) {
		return GroupPreview{}, ErrInvalidGroupInfo
	}
	g, err := s.repo.Update(ctx, groupID, actor, req.GroupName, req.Description, req.Visibility)
	if err != nil {
		return GroupPreview{}, err
	}
	s.invalidate(ctx, groupID)
	s.pushMembers(ctx, groupID, actor, "info", uuid.Nil)
	return g, nil
}

func (s *groupService) TransferAdmin(ctx context.Context, groupID, admin, target uuid.UUID) error {
	if err := s.repo.TransferAdmin(ctx, groupID, admin, target); err != nil {
		return err
	}
	s.invalidate(ctx, groupID)
	s.pushMembers(ctx, groupID, admin, "admin", target)
	return nil
}

func (s *groupService) SetRole(ctx context.Context, groupID, admin, target uuid.UUID, req roleRequest) error {
	if req.Role != RoleModerator && req.Role != RoleMember {
		return ErrBadRole
	}
	perms, err := cleanPermissions(req.Permissions)
	if err != nil {
		return err
	}
	if err := s.repo.SetRole(ctx, groupID, admin, target, req.Role, perms); err != nil {
		return err
	}
	s.invalidate(ctx, groupID)
	s.pushMembers(ctx, groupID, admin, "role", target)
	return nil
}

func (s *groupService) Delete(ctx context.Context, groupID, admin uuid.UUID) error {
	members, err := s.repo.Delete(ctx, groupID, admin)
	if err != nil {
		return err
	}
	s.invalidate(ctx, groupID)
	for _, fn := range s.onDelete {
		fn(ctx, groupID)
	}
	others := slices.DeleteFunc(members, func(id uuid.UUID) bool { return id == admin })
	s.pushTo(ctx, others, ws.EventGroupGone, ws.OutGroupRemoved{GroupID: groupID.String(), Reason: "deleted"})
	return nil
}

func (s *groupService) OnDelete(fn func(ctx context.Context, groupID uuid.UUID)) {
	s.onDelete = append(s.onDelete, fn)
}

// pushMembers tells the group's members (except the one who acted) what changed.
func (s *groupService) pushMembers(ctx context.Context, groupID, actor uuid.UUID, change string, user uuid.UUID) {
	members, err := s.Members(ctx, groupID)
	if err != nil {
		s.logger.WarnContext(ctx, "failed to load members for group event", "error", err)
		return
	}
	ev := ws.OutGroupUpdated{GroupID: groupID.String(), Change: change}
	if user != uuid.Nil {
		ev.UserID = user.String()
	}
	s.pushTo(ctx, slices.DeleteFunc(members, func(id uuid.UUID) bool { return id == actor }), ws.EventGroupUpd, ev)
}

func (s *groupService) pushTo(ctx context.Context, to []uuid.UUID, event string, payload any) {
	if len(to) == 0 || s.push == nil {
		return
	}
	if err := s.push.WriteToUsers(to, ws.Event{Event: event, Payload: payload}); err != nil {
		s.logger.WarnContext(ctx, "failed to push group event", "event", event, "error", err)
	}
}

func (s *groupService) GroupName(ctx context.Context, groupID uuid.UUID) (string, error) {
	snap, err := s.snapshot(ctx, groupID)
	return snap.Name, err
}

func (s *groupService) SetFriends(f FriendChecker)  { s.friends = f }
func (s *groupService) SetNotifier(n push.Notifier) { s.notify = n }

func (s *groupService) AddFriend(ctx context.Context, groupID, actor, target uuid.UUID) error {
	// permission first, so outsiders learn nothing (404) whoever they name
	if err := s.check(ctx, groupID, actor, PermAddMembers); err != nil {
		return err
	}
	if in, err := s.IsMember(ctx, groupID, target); err != nil || in {
		if in {
			return ErrAlreadyMember
		}
		return err
	}
	ok, err := s.friends.AreFriends(ctx, actor, target)
	if err != nil {
		return err
	}
	if !ok { // blocking ends a friendship, so blocks are covered too
		return ErrNotFriend
	}
	if err := s.repo.AddMember(ctx, groupID, actor, target); err != nil {
		return err
	}
	s.invalidate(ctx, groupID)
	s.pushTo(ctx, []uuid.UUID{target}, ws.EventGroupAdded, ws.OutGroupAdded{GroupID: groupID.String(), ByID: actor.String()})
	s.pushMembers(ctx, groupID, actor, "joined", target)
	if s.notify != nil {
		s.notify.Notify(ctx, push.Job{Kind: push.KindGroupAdded, To: []uuid.UUID{target}, From: actor, Group: groupID})
	}
	return nil
}

func (s *groupService) SetAvatar(ctx context.Context, groupID, actor uuid.UUID, raw []byte) (*string, error) {
	img, err := users.ProcessAvatar(raw)
	if err != nil {
		return nil, err
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.repo.SetAvatar(ctx, groupID, actor, img, at); err != nil {
		return nil, err
	}
	s.pushMembers(ctx, groupID, actor, "info", uuid.Nil)
	return AvatarURL(groupID, pgtype.Timestamp{Time: at, Valid: true}), nil
}

func (s *groupService) DeleteAvatar(ctx context.Context, groupID, actor uuid.UUID) error {
	if err := s.repo.DeleteAvatar(ctx, groupID, actor); err != nil {
		return err
	}
	s.pushMembers(ctx, groupID, actor, "info", uuid.Nil)
	return nil
}

func (s *groupService) Avatar(ctx context.Context, groupID, viewer uuid.UUID) ([]byte, time.Time, error) {
	return s.repo.Avatar(ctx, groupID, viewer)
}

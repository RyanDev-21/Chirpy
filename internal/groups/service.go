package groups

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/ws"
	"github.com/google/uuid"
)

type GroupService interface {
	CreateGroup(ctx context.Context, creatorID uuid.UUID, req createGroupRequest) (*GroupInfo, error)
	JoinGroup(ctx context.Context, groupID, userID uuid.UUID) error
	LeaveGroup(ctx context.Context, groupID, userID uuid.UUID) error
	MyGroups(ctx context.Context, userID uuid.UUID) ([]GroupSummary, error)
	GroupDetail(ctx context.Context, groupID, viewer uuid.UUID) (GroupDetail, error)
	// PreviewInvite shows what a code leads to before joining.
	PreviewInvite(ctx context.Context, code string) (GroupPreview, error)
	JoinByInvite(ctx context.Context, userID uuid.UUID, code string) (uuid.UUID, error)
	// InviteCode is visible to members; RegenerateInvite is leader-only.
	InviteCode(ctx context.Context, groupID, viewer uuid.UUID) (string, error)
	RegenerateInvite(ctx context.Context, groupID, userID uuid.UUID) (string, error)
	// IsMember and Members are used by chat for authorization and fan-out.
	IsMember(ctx context.Context, groupID, userID uuid.UUID) (bool, error)
	Members(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)

	// Leader-only administration.
	Kick(ctx context.Context, groupID, leader, target uuid.UUID) error
	Update(ctx context.Context, groupID, leader uuid.UUID, req updateGroupRequest) (GroupPreview, error)
	TransferLeader(ctx context.Context, groupID, leader, target uuid.UUID) error
	Delete(ctx context.Context, groupID, leader uuid.UUID) error
	// OnDelete registers cleanup to run after a group is deleted (the chat
	// package drops its cached messages).
	OnDelete(fn func(ctx context.Context, groupID uuid.UUID))
	// GroupName is the group's display name (cached).
	GroupName(ctx context.Context, groupID uuid.UUID) (string, error)
}

// Pusher delivers realtime events (implemented by *ws.Hub).
type Pusher interface {
	WriteToUsers(targets []uuid.UUID, ev ws.Event) error
}

type groupService struct {
	repo     GroupRepo
	store    Store
	push     Pusher
	onDelete []func(ctx context.Context, groupID uuid.UUID)
	logger   *slog.Logger
}

func NewGroupService(repo GroupRepo, store Store, push Pusher, logger *slog.Logger) GroupService {
	return &groupService{repo: repo, store: store, push: push, logger: logger}
}

func validateCreate(req *createGroupRequest) error {
	req.GroupName = strings.TrimSpace(req.GroupName)
	n := utf8.RuneCountInString(req.GroupName)
	if n == 0 || n > maxGroupNameLen || utf8.RuneCountInString(req.Description) > maxDescLen {
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
	return nil
}

func (s *groupService) LeaveGroup(ctx context.Context, groupID, userID uuid.UUID) error {
	if err := s.repo.Leave(ctx, groupID, userID); err != nil {
		return err
	}
	s.invalidate(ctx, groupID)
	return nil
}

func (s *groupService) MyGroups(ctx context.Context, userID uuid.UUID) ([]GroupSummary, error) {
	return s.repo.ListForUser(ctx, userID)
}

// GroupDetail is members-only: groups are private, so a non-member gets
// ErrNotFoundGroup rather than learning the group exists.
func (s *groupService) GroupDetail(ctx context.Context, groupID, viewer uuid.UUID) (GroupDetail, error) {
	d, err := s.repo.Detail(ctx, groupID, viewer)
	if err != nil {
		return GroupDetail{}, err
	}
	if !d.IsMember {
		return GroupDetail{}, ErrNotFoundGroup
	}
	return d, nil
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
	snap, err := s.snapshot(ctx, groupID)
	if err != nil {
		return "", err
	}
	if !slices.Contains(snap.Members, userID) {
		return "", ErrNotFoundGroup
	}
	if snap.LeaderID != userID {
		return "", ErrNotLeader
	}
	code, err := s.repo.RegenerateInviteCode(ctx, groupID)
	return FormatInviteCode(code), err
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

func (s *groupService) invalidate(ctx context.Context, groupID uuid.UUID) {
	if err := s.store.Invalidate(ctx, groupID.String()); err != nil {
		s.logger.ErrorContext(ctx, "failed to invalidate group cache", "group", groupID, "error", err)
	}
}

// --- leader-only administration ---

func (s *groupService) Kick(ctx context.Context, groupID, leader, target uuid.UUID) error {
	if err := s.repo.Kick(ctx, groupID, leader, target); err != nil {
		return err
	}
	s.invalidate(ctx, groupID)
	s.pushTo(ctx, []uuid.UUID{target}, ws.EventGroupGone, ws.OutGroupRemoved{GroupID: groupID.String(), Reason: "kicked"})
	s.pushMembers(ctx, groupID, leader, "members", target)
	return nil
}

func (s *groupService) Update(ctx context.Context, groupID, leader uuid.UUID, req updateGroupRequest) (GroupPreview, error) {
	if req.GroupName == nil && req.Description == nil {
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
	g, err := s.repo.Update(ctx, groupID, leader, req.GroupName, req.Description)
	if err != nil {
		return GroupPreview{}, err
	}
	s.invalidate(ctx, groupID)
	s.pushMembers(ctx, groupID, leader, "info", uuid.Nil)
	return g, nil
}

func (s *groupService) TransferLeader(ctx context.Context, groupID, leader, target uuid.UUID) error {
	if err := s.repo.TransferLeader(ctx, groupID, leader, target); err != nil {
		return err
	}
	s.invalidate(ctx, groupID)
	s.pushMembers(ctx, groupID, leader, "leader", target)
	return nil
}

func (s *groupService) Delete(ctx context.Context, groupID, leader uuid.UUID) error {
	members, err := s.repo.Delete(ctx, groupID, leader)
	if err != nil {
		return err
	}
	s.invalidate(ctx, groupID)
	for _, fn := range s.onDelete {
		fn(ctx, groupID)
	}
	others := slices.DeleteFunc(members, func(id uuid.UUID) bool { return id == leader })
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

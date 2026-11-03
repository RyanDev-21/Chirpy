package groups

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

type GroupService interface {
	CreateGroup(ctx context.Context, creatorID uuid.UUID, req createGroupRequest) (*GroupInfo, error)
	JoinGroup(ctx context.Context, groupID, userID uuid.UUID) error
	LeaveGroup(ctx context.Context, groupID, userID uuid.UUID) error
	MyGroups(ctx context.Context, userID uuid.UUID) ([]GroupSummary, error)
	GroupDetail(ctx context.Context, groupID, viewer uuid.UUID) (GroupDetail, error)
	// IsMember and Members are used by chat for authorization and fan-out.
	IsMember(ctx context.Context, groupID, userID uuid.UUID) (bool, error)
	Members(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)
}

type groupService struct {
	repo   GroupRepo
	store  Store
	logger *slog.Logger
}

func NewGroupService(repo GroupRepo, store Store, logger *slog.Logger) GroupService {
	return &groupService{repo: repo, store: store, logger: logger}
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

func (s *groupService) GroupDetail(ctx context.Context, groupID, viewer uuid.UUID) (GroupDetail, error) {
	return s.repo.Detail(ctx, groupID, viewer)
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

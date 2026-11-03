package groups

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"github.com/google/uuid"
)

type GroupService interface {
	CreateGroup(ctx context.Context, creatorID uuid.UUID, req createGroupRequest) (*GroupInfo, error)
	JoinGroup(ctx context.Context, groupID, userID uuid.UUID) error
	LeaveGroup(ctx context.Context, groupID, userID uuid.UUID) error
	// IsMember and Members are used by chat for authorization and fan-out.
	IsMember(ctx context.Context, groupID, userID uuid.UUID) (bool, error)
	Members(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)

	HandleCreate(ctx context.Context, j JobCreate) error
	HandleAddMember(ctx context.Context, j JobMember) error
	HandleRemoveMember(ctx context.Context, j JobMember) error
}

type groupService struct {
	repo   GroupRepo
	store  Store
	queue  queue.Publisher
	logger *slog.Logger
}

func NewGroupService(repo GroupRepo, store Store, q queue.Publisher, logger *slog.Logger) GroupService {
	return &groupService{repo: repo, store: store, queue: q, logger: logger}
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

// ensureLoaded copies a group from postgres into redis on first use.
func (s *groupService) ensureLoaded(ctx context.Context, groupID uuid.UUID) error {
	ok, err := s.store.Loaded(ctx, groupID)
	if err != nil || ok {
		return err
	}
	snap, err := s.repo.Snapshot(ctx, groupID)
	if err != nil {
		return err
	}
	return s.store.Load(ctx, groupID, snap)
}

func (s *groupService) CreateGroup(ctx context.Context, creatorID uuid.UUID, req createGroupRequest) (*GroupInfo, error) {
	if err := validateCreate(&req); err != nil {
		return nil, err
	}
	taken, err := s.repo.NameTaken(ctx, req.GroupName)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, ErrDuplicateName
	}
	groupID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	reserved, err := s.store.ReserveName(ctx, req.GroupName, groupID)
	if err != nil {
		return nil, err
	}
	if !reserved {
		return nil, ErrDuplicateName
	}
	if err := s.store.Load(ctx, groupID, GroupSnapshot{
		Name:     req.GroupName,
		MaxMem:   req.MaxMems,
		LeaderID: creatorID,
		Members:  []uuid.UUID{creatorID},
	}); err != nil {
		_ = s.store.ReleaseName(ctx, req.GroupName)
		return nil, err
	}
	if err := s.publish(ctx, TopicCreateGroup, JobCreate{
		GroupID:     groupID,
		CreatorID:   creatorID,
		Name:        req.GroupName,
		Description: req.Description,
		MaxMembers:  req.MaxMems,
	}); err != nil {
		return nil, err
	}
	return &GroupInfo{ChatID: groupID}, nil
}

func (s *groupService) JoinGroup(ctx context.Context, groupID, userID uuid.UUID) error {
	if err := s.ensureLoaded(ctx, groupID); err != nil {
		return err
	}
	if err := s.store.Join(ctx, groupID, userID); err != nil {
		return err
	}
	return s.publish(ctx, TopicAddMember, JobMember{GroupID: groupID, UserID: userID})
}

func (s *groupService) LeaveGroup(ctx context.Context, groupID, userID uuid.UUID) error {
	if err := s.ensureLoaded(ctx, groupID); err != nil {
		return err
	}
	if err := s.store.Leave(ctx, groupID, userID); err != nil {
		return err
	}
	return s.publish(ctx, TopicRemoveMember, JobMember{GroupID: groupID, UserID: userID})
}

func (s *groupService) IsMember(ctx context.Context, groupID, userID uuid.UUID) (bool, error) {
	if err := s.ensureLoaded(ctx, groupID); err != nil {
		return false, err
	}
	return s.store.IsMember(ctx, groupID, userID)
}

func (s *groupService) Members(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	if err := s.ensureLoaded(ctx, groupID); err != nil {
		return nil, err
	}
	return s.store.Members(ctx, groupID)
}

func (s *groupService) publish(ctx context.Context, topic string, payload any) error {
	pubCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return s.queue.Publish(pubCtx, topic, payload)
}

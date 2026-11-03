package groups

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const RoleLeader = "Leader"

var ErrLeaderCannotLeave = errors.New("group leader cannot leave the group")

type GroupService interface {
	createGroup(ctx context.Context, createrID uuid.UUID, groupInfo *createGroupStruct) (*GroupInfo, error)
	joinGroup(ctx context.Context, groupID uuid.UUID, userID uuid.UUID) error
	leaveGroup(ctx context.Context, groupID uuid.UUID, userID uuid.UUID) error

	StartWorkerForCreateGroup(channel chan *mq.Channel)
	StartWorkerForAddMember(channel chan *mq.Channel)
	StartWorkerForLeaveMember(channel chan *mq.Channel)
}

type groupService struct {
	groupCache *Cache
	groupRepo  GroupRepo
	mq         *mq.MainMQ
	logger     *slog.Logger
}

func NewGroupService(groupRepo GroupRepo, mq *mq.MainMQ, groupCache *Cache, logger *slog.Logger) GroupService {
	return &groupService{
		groupRepo:  groupRepo,
		mq:         mq,
		groupCache: groupCache,
		logger:     logger,
	}
}

func validateCreateGroup(g *createGroupStruct) error {
	g.GroupName = strings.TrimSpace(g.GroupName)
	if g.GroupName == "" || len(g.GroupName) > maxGroupNameLen {
		return ErrInvalidGroupReq
	}
	if len(g.Description) > maxDescLen {
		return ErrInvalidGroupReq
	}
	if g.MaxMems < 2 || g.MaxMems > maxGroupSize {
		return ErrInvalidGroupReq
	}
	return nil
}

// createGroup registers the group in the cache, makes the creator its leader
// and queues the db writes.
func (s *groupService) createGroup(ctx context.Context, createrID uuid.UUID, groupInfo *createGroupStruct) (*GroupInfo, error) {
	if err := validateCreateGroup(groupInfo); err != nil {
		return nil, err
	}
	exists, err := s.groupCache.CheckGroupNameFromCache(groupInfo.GroupName)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrDuplicateName
	}
	chatID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	groupInfo.CurrentMem = 1
	s.groupCache.AddGroup(chatID, CacheGroupInfo{
		Name:     groupInfo.GroupName,
		TotalMem: groupInfo.CurrentMem,
		MaxMem:   groupInfo.MaxMems,
	}, []uuid.UUID{createrID})

	if err := s.publish(ctx, TopicCreateGroup, GroupPublish{
		GroupID:   GroupInfo{chatID},
		CreatorID: createrID,
		GroupInfo: *groupInfo,
		Role:      RoleLeader,
	}); err != nil {
		return nil, err
	}
	return &GroupInfo{ChatID: chatID}, nil
}

func (s *groupService) joinGroup(ctx context.Context, groupID uuid.UUID, userID uuid.UUID) error {
	if err := s.groupCache.AddMember(groupID, userID); err != nil {
		return err
	}
	if err := s.publish(ctx, TopicAddMember, ManageGroupPublishStruct{GroupId: groupID, UserID: userID}); err != nil {
		return err
	}
	return nil
}

func (s *groupService) leaveGroup(ctx context.Context, groupID uuid.UUID, userID uuid.UUID) error {
	role, err := s.groupRepo.getMemberRole(ctx, groupID, userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if role == RoleLeader {
		return ErrLeaderCannotLeave
	}
	if err := s.groupCache.RemoveMember(groupID, userID); err != nil {
		return err
	}
	if err := s.publish(ctx, TopicRemoveMember, ManageGroupPublishStruct{GroupId: groupID, UserID: userID}); err != nil {
		return err
	}
	return nil
}

func (s *groupService) publish(ctx context.Context, topic string, payload any) error {
	pubCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.mq.PublishWithContext(pubCtx, topic, payload); err != nil {
		s.logger.Error("failed to publish group job", "topic", topic, "err", err)
		return err
	}
	return nil
}

package groups

import (
	"context"
	"time"

	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
)

func (s *groupService) StartWorkerForCreateGroup(channel chan *mq.Channel) {
	for chen := range channel {
		msg := chen.Msg.(GroupPublish)
		if err := s.groupRepo.createGroup(msg.GroupID.ChatID, msg.GroupInfo); err != nil {
			s.logger.Error("failed to create the group", "err", err)
			chen.RetriesCount++
			s.mq.Republish(chen, chen.RetriesCount)
			continue
		}
		if err := s.groupRepo.createGroupLeader(creatorPublishStruct{
			GroupID: msg.GroupID.ChatID,
			UserID:  msg.CreatorID,
			Role:    msg.Role,
		}); err != nil {
			s.logger.Error("failed to create the group leader", "err", err)
			chen.RetriesCount++
			s.mq.Republish(chen, chen.RetriesCount)
			continue
		}
	}
}

func (s *groupService) StartWorkerForAddMember(channel chan *mq.Channel) {
	for chen := range channel {
		msg := chen.Msg.(ManageGroupPublishStruct)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := s.groupRepo.addMember(ctx, &database.AddMemberParams{GroupID: msg.GroupId, MemberID: msg.UserID})
		cancel()
		if err != nil {
			s.logger.Error("failed to add member", "err", err)
			chen.RetriesCount++
			s.mq.Republish(chen, chen.RetriesCount)
		}
	}
}

func (s *groupService) StartWorkerForLeaveMember(channel chan *mq.Channel) {
	for chen := range channel {
		msg := chen.Msg.(ManageGroupPublishStruct)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := s.groupRepo.deleteMember(ctx, &database.DeleteMemFromGroupParams{GroupID: msg.GroupId, MemberID: msg.UserID})
		cancel()
		if err != nil {
			s.logger.Error("failed to remove the member from the group", "err", err)
			chen.RetriesCount++
			s.mq.Republish(chen, chen.RetriesCount)
		}
	}
}

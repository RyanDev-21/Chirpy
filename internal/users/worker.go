package users

import (
	"log"

	mq "RyanDev-21.com/Chirpy/internal/customMq"
)

func (s *userService) StartWorkerForAddFri(channel chan *mq.Channel) {
	for chen := range channel {
		msg := chen.Msg.(*FriendReq)

		err := s.userRepo.SendFriendRequest(msg.FromID, msg.ToID, msg.ReqID)
		if err != nil {
			chen.RetriesCount++
			s.mainMq.Republish(chen, chen.RetriesCount)
		}
		continue
	}
	log.Printf("successfully created the add fri record")
}

func (s *userService) StartWorkerForConfirmFri(channel chan *mq.Channel) {
	for chen := range channel {
		msg := chen.Msg.(*FriendReq)
		err := s.userRepo.UpdateFriReq(msg.ReqID, msg.FromID, msg.UpdateTime)
		if err != nil {
			chen.RetriesCount++
			s.mainMq.Republish(chen, chen.RetriesCount)
		}
		continue
	}
	log.Printf("successfully created the add fri record")
}

func (s *userService) StartWorkerForCancelReq(channel chan *mq.Channel) {
	for chen := range channel {
		msg := chen.Msg.(*CancelFriendReq)
		err := s.userRepo.CancelFriReq(msg.ReqID, msg.FromID, msg.UpdateTime)
		if err != nil {
			chen.RetriesCount++
			s.mainMq.Republish(chen, chen.RetriesCount)
		}
		continue
	}
	log.Printf("successfully updated the status to cancel")
}

func (s *userService) StartWorkerForDeleteReq(channel chan *mq.Channel) {
	for chen := range channel {
		msg := chen.Msg.(*DeleteFirReqStruct)
		err := s.userRepo.DeleteFriReq(msg.ReqID, msg.FromID)
		if err != nil {
			chen.RetriesCount++
			s.mainMq.Republish(chen, chen.RetriesCount)
		}
		continue
	}
	log.Printf("successfully delete the req record")
}

func (s *userService) StartWorkerForUpdateUserCache(channel chan *mq.Channel) {
	for chen := range channel {
		msg := chen.Msg.(*[]User)
		for _, v := range *msg {
			s.userCache.UpdateUserCache(&v)
		}
	}

	log.Printf("successfully updated the userCache")
}

func (s *userService) StartWorkerForSaveConfig(channel chan *mq.Channel) {
	for chen := range channel {
		msg := chen.Msg.(JobForSaveConfig)
		err := s.userRepo.SaveEleConfig(msg.UserID, &msg.ConfigList.List)
		if err != nil {
			log.Printf("err value:%v", err)
			chen.RetriesCount++
			s.mainMq.Republish(chen, chen.RetriesCount)
		}
		continue
	}
	log.Printf("successfully added the req record")
}

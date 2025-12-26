package groups

import (
	"context"
	"fmt"
	"log"

	chatmodel "RyanDev-21.com/Chirpy/internal/chat/chatModel"
	"github.com/google/uuid"
)

type GroupService interface{
	createGroup(ctx context.Context,createrID uuid.UUID,groupMembers *createGroupRequest)(*GroupInfo,error)
	joinGroup(ctx context.Context,groupID uuid.UUID,userID uuid.UUID)error
}


type groupService struct{
	groupRepo GroupRepo
	hub *chatmodel.Hub
}


func NewGroupService(groupRepo GroupRepo,hub *chatmodel.Hub)GroupService{
	return &groupService{
		groupRepo: groupRepo,
		hub : hub,
	}
}

//get new chatID and store it in the db and return the groupInfo struct
func (s *groupService)createGroup(ctx context.Context,createrID uuid.UUID,groupMembers *createGroupRequest)(*GroupInfo,error){
	chatID, err := uuid.NewUUID()
	if err !=nil{
		return nil,err
	}
	err = s.groupRepo.createChatRecord(ctx,chatID,groupMembers.Members)
	if err !=nil{
		return nil,err
	}
	return &GroupInfo{
		ChatID: chatID,
	},nil
}

func (s *groupService)joinGroup(ctx context.Context,groupID uuid.UUID,userID uuid.UUID)error{
	fmt.Println("saved into the db frist")
	joinStruct := chatmodel.GroupActionInfo{
		GroupID: groupID,
		UserID: userID,
	}
	//send the struct through the channel of the hub
	select{
	case s.hub.JoinChan<-joinStruct:
	case <-ctx.Done():
	return ctx.Err()
	default:
		log.Println("warning: hub channel is full message dropped")
	}
	return nil
}






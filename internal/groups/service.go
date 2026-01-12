package groups

import (
	"context"
//	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	chatmodel "RyanDev-21.com/Chirpy/internal/chat/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"

	//rabbitmq "RyanDev-21.com/Chirpy/internal/rabbitMq"
	"github.com/google/uuid"
)

type GroupService interface{
	createGroup(ctx context.Context,createrID uuid.UUID,groupMembers *createGroupRequest)(*GroupInfo,error)
	joinGroup(ctx context.Context,groupID uuid.UUID,userID uuid.UUID)error
	leaveGroup(ctx context.Context,groupID uuid.UUID,userID uuid.UUID)error
	 StartWorkerForCreateGroup(channel chan *mq.Channel)
	StartWorkerForCreateGroupLeader(channel chan *mq.Channel)
}


var ErrDuplicateName = errors.New("duplicate name")

//both join and leave share the same struct type
type groupService struct{
	groupCache *GroupCache
	groupRepo GroupRepo
	hub *chatmodel.Hub
	mq *mq.MainMQ
}



func NewGroupService(groupRepo GroupRepo,hub *chatmodel.Hub,mq *mq.MainMQ,groupCache *GroupCache)GroupService{
	return &groupService{
		groupRepo: groupRepo,
		hub : hub,
		mq: mq,
		groupCache: groupCache,
	}
}

//get new chatID and store it in the db and return the groupInfo struct
//need to fix this one when the group has the same name it is kinda returning the new id
//NOTE:: have to rethink about the createGroup right now if user create a group then his id got added to the mem-table with role("Leader")
func (s *groupService)createGroup(ctx context.Context,createrID uuid.UUID,groupInfo *createGroupRequest)(*GroupInfo,error){
	chatID, err := uuid.NewUUID()
	if err !=nil{
		return nil,err
	}
	//check the name first 
	ok,err := s.groupCache.CheckGroupNameFromCache(groupInfo.GroupName)
	if err !=nil {
		return nil,err
	}
	if !ok{
		return nil,ErrDuplicateName
	}
	//store newly created groupID and its member list
	//before saving into the db we first publish it into the queue stack
	payload , err := json.Marshal(GroupPublish{
		GroupID: GroupInfo{chatID},
		GroupInfo: *groupInfo,
	})
	if err !=nil{
		return nil,err
	}
	log.Printf("okay now publishing the payload")
	//publsih two jobs for the db operations
	//in the first one i marshal it so that it becomes bytes 
	// didn't marshal it so it becomes the struct
	s.mq.Publish("createGroup",payload)
	s.mq.Publish("addCreator",creatorPublishStruct{
		GroupID: chatID,	
		UserID: createrID,
		Role: "Leader",
	})	

	return &GroupInfo{
		ChatID: chatID,
	},nil
}

//NOTE::you really need centralized encoder and decoder
//there is a code duplication in this fucntion
func (s *groupService)StartWorkerForCreateGroup(channel chan *mq.Channel){
	for chen := range channel{
		//if this is not the valid type then the pipeline will break
		jsonBytes := chen.Msg.([]byte)
		var msg GroupPublish
		 err := json.Unmarshal(jsonBytes,&msg)
		if err !=nil{

			chen.RetriesCount ++
		 s.mq.Republish(chen,chen.RetriesCount)		

		}
		
		groupID := msg.GroupID.ChatID
		groupInfo := msg.GroupInfo
		err = s.groupRepo.createGroup(groupID,createGroupRequest{
			GroupName: groupInfo.GroupName,
			Description: groupInfo.Description,
			MaxMems: groupInfo.MaxMems ,
		})

		if err !=nil{

			chen.RetriesCount ++
		 s.mq.Republish(chen,chen.RetriesCount)		
			return
		}
		log.Printf("Successfully created the group")
	}	

}
func (s *groupService)StartWorkerForCreateGroupLeader(channel chan *mq.Channel){
	for chen := range channel{
		//if this is not the valid type then the pipeline will break
		msg := chen.Msg.(creatorPublishStruct)	
		err := s.groupRepo.createGroupLeader(creatorPublishStruct{
				GroupID: msg.GroupID,
				UserID: msg.UserID,
				Role: msg.Role,
		})

		if err !=nil{
			log.Printf("error reason: %v",err)
			chen.RetriesCount ++
		 s.mq.Republish(chen,chen.RetriesCount)		
			return	
		}
		log.Printf("Doned the group leader worker %v",chen.LocalTag)
	}	

}


//might have to refactor these two service into one service which operate based on the type of the service
//send the joinStruct to the JoinChan to ativate the function of the hub
//TODO:right now haven't stored the generated groupID in db so we can basically add the invalid id and will still work
//need to fix that
func (s *groupService)joinGroup(ctx context.Context,groupID uuid.UUID,userID uuid.UUID)error{
	groupInfo, err := s.groupCache.GetGroupInfoFromGroupCache(groupID)
	if err !=nil{
		return err
	}
	if groupInfo.total_mem ==groupInfo.max_mem{
		return ErrGroupFull	
	}	
	//assign the job for the db operation of adding member
	//NOTE::maybe implement member cache
	s.mq.Publish("manageGroupMembers",&ManageGroupPublishStruct{
		GroupId: groupID,
		UserID: userID,
	})		

	joinStruct := chatmodel.GroupActionInfo{
		GroupID: groupID,
		UserID: userID,
	}
	//send the struct through the channel of the hub
	select{
	case s.hub.JoinChan<-joinStruct:
	case <-ctx.Done():
	return ctx.Err()
	//in case of failing to send to the channel 
	//eg.too long to send or the channel is blocked
	//or misformed
	default:
		log.Println("warning: hub channel is full message dropped")
	}
	return nil
}

func (s *groupService)leaveGroup(ctx context.Context,groupID uuid.UUID,userID uuid.UUID)error{
	//saving into the db should be different from the join one
	fmt.Println("saved into the db")
	leaveStruct := chatmodel.GroupActionInfo{
		GroupID: groupID,
		UserID: userID,
	}

	//don't really like this duplicate thing

	select{
	case s.hub.LeaveChan<-leaveStruct:
	case <-ctx.Done():
	return ctx.Err()
	//in case of failing to send to the channel 
	//eg.too long to send or the channel is blocked
	//or misformed
	default:
		log.Println("warning: hub channel is full message dropped")
	}
	return nil


}

// func (s *groupService)startCreateGroupWorker()error{
//
// }


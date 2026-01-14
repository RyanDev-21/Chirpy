package groups

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	//	"fmt"
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
	StartWorkerForAddMemberList(channel chan *mq.Channel)
	StartWorkerForAddMember(channel chan *mq.Channel)
		
}


var ErrDuplicateName = errors.New("duplicate name")

//both join and leave share the same struct type
type groupService struct{
	groupCache *Cache
	groupRepo GroupRepo
	hub *chatmodel.Hub
	mq *mq.MainMQ
}



func NewGroupService(groupRepo GroupRepo,hub *chatmodel.Hub,mq *mq.MainMQ,groupCache *Cache)GroupService{
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
	valid,err := s.groupCache.CheckGroupNameFromCache(groupInfo.GroupName)
	if err !=nil {
		log.Printf("failed to check the name #%s#",err)
		return nil,err
	}
	if valid{
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
	s.mq.Publish("createGroup",payload)
	//job for the db op
	s.mq.Publish("addCreator",creatorPublishStruct{
		GroupID: chatID,	
		UserID: createrID,
		Role: "Leader",
	})	
	//for updating the cache and for db
	userIdsList :=append(groupInfo.Members,createrID);
	s.mq.Publish("addMember",membersPubStruct{
		UserIds: userIdsList,
		GroupId: chatID,
	})

	return &GroupInfo{
		ChatID: chatID,
	},nil
}



//might have to refactor these two service into one service which operate based on the type of the service
//send the joinStruct to the JoinChan to ativate the function of the hub
//TODO:right now haven't stored the generated groupID in db so we can basically add the invalid id and will still work
//need to fix that
func (s *groupService)joinGroup(ctx context.Context,groupID uuid.UUID,userID uuid.UUID)error{
	groupInfo, err := s.groupCache.GetFromGroup(groupID)
	if err !=nil{
		return err
	}
	if groupInfo.totalMem ==groupInfo.maxMem{
		return ErrGroupFull	
	}	
	//assign the job for the db operation of adding member
	//NOTE::maybe implement member cache
	s.mq.Publish("manageGroupMembers",&ManageGroupPublishStruct{
		GroupId: groupID,
		UserID: userID,
		Action: "Join",
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

//should separate the normal mem leaveGroup with leader leaveGroup
//need to think about whether i want my service to do the cache or the worker to do it
func (s *groupService)leaveGroup(ctx context.Context,groupID uuid.UUID,userID uuid.UUID)error{
	//saving into the db should be different from the join one
	leaveStruct := chatmodel.GroupActionInfo{
		GroupID: groupID,
		UserID: userID,
	}
	//firts update the group metadata first 
	go func(gpID *uuid.UUID){
	s.groupCache.groupMuLock.Lock()
	s.groupCache.GroupCache[*gpID].totalMem -=1
	s.groupCache.groupMuLock.Unlock()
	}(&groupID)
	 
	//now we update the member list of that group
	go func(gpID *uuid.UUID,userID *uuid.UUID){
		s.groupCache.memMuLock.Lock()
		memberIdsList := *s.groupCache.MemberCache[*gpID]
		//you need to know the index
		updatedMemberIdsList := func()[]uuid.UUID{
			index := slices.Index(memberIdsList,*userID)
			memberIdsList[index] = memberIdsList[len(memberIdsList)-1]
			log.Printf("memberIdsList value: %v",memberIdsList)
			return memberIdsList
		}
		log.Printf("finished updating in the cache : %v",updatedMemberIdsList())
		*s.groupCache.MemberCache[*gpID] = updatedMemberIdsList()
		
	}(&groupID,&userID)


	//and then we publish the job for the db worker to consume
	s.mq.Publish("manageGroupMembers",&ManageGroupPublishStruct{
		GroupId: groupID,
		UserID: userID,
		Action: "Leave",
	})		
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


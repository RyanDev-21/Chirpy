package groups

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	//	"fmt"
	"log"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"

	//rabbitmq "RyanDev-21.com/Chirpy/internal/rabbitMq"
	"github.com/google/uuid"
)

//WARNING:need to abstract out the caching function


type GroupService interface{
	createGroup(ctx context.Context,createrID uuid.UUID,groupMembers *createGroupStruct)(*GroupInfo,error)
	joinGroup(ctx context.Context,groupID uuid.UUID,userID uuid.UUID)error
	leaveGroup(ctx context.Context,groupID uuid.UUID,userID uuid.UUID)error
	//need to abstract out worker logic from service	
	StartWorkerForCreateGroup(channel chan *mq.Channel)
//	StartWorkerForCreateGroupLeader(channel chan *mq.Channel)
	StartWorkerForAddMemberList(channel chan *mq.Channel)
	StartWorkerForAddMember(channel chan *mq.Channel)
	StartWorkerForLeaveMember(channel chan *mq.Channel)
}


var ErrDuplicateName = errors.New("duplicate name")

//both join and leave share the same struct type
type groupService struct{
	groupCache *Cache
	groupRepo GroupRepo
	hub *chatmodel.Hub
	mq *mq.MainMQ
	logger *slog.Logger
}



func NewGroupService(groupRepo GroupRepo,hub *chatmodel.Hub,mq *mq.MainMQ,groupCache *Cache,logger *slog.Logger)GroupService{
	return &groupService{
		groupRepo: groupRepo,
		hub : hub,
		mq: mq,
		groupCache: groupCache,
		logger: logger,	
	}
}

//get new chatID and store it in the db and return the groupInfo struct
//need to fix this one when the group has the same name it is kinda returning the new id
//NOTE:: have to rethink about the createGroup right now if user create a group then his id got added to the mem-table with role("Leader")
func (s *groupService)createGroup(ctx context.Context,createrID uuid.UUID,groupInfo *createGroupStruct)(*GroupInfo,error){
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
	//updated group member count
	memCount := int16(len(groupInfo.Members))+1;//the creator id should count too +1
	groupInfo.CurrentMem= memCount	
	//update the metadata in the cache	
	go func(groupID uuid.UUID,groupInfo *createGroupStruct){
		s.groupCache.groupMuLock.Lock()
		s.groupCache.GroupCache[chatID] = &CacheGroupInfo{
		 Name: groupInfo.GroupName,
		 TotalMem: groupInfo.CurrentMem,	
		MaxMem: groupInfo.MaxMems,
		}	
		s.groupCache.groupMuLock.Unlock()
	}(chatID,groupInfo)
	//need to add the creatorId into the memberIds 	
	updatedMemList :=append(groupInfo.Members,createrID);
	//now update the member list
	go func(groupID uuid.UUID,memberIds *[]uuid.UUID){
		s.groupCache.memMuLock.Lock()
		s.groupCache.MemberCache[chatID] = memberIds
		s.groupCache.memMuLock.Unlock()
	}(chatID,&updatedMemList)


	payload  :=GroupPublish{
		GroupID: GroupInfo{chatID},
		 CreatorID:createrID,
		GroupInfo: *groupInfo,
		Role: "Leader",
	}
	
	//create three job for db operations
	s.mq.Publish("createGroup",payload)
	s.mq.Publish("addChunkMembers",membersPubStruct{
		UserIds: updatedMemList,
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

	//first check whether the gp is full or not
	if groupInfo.TotalMem ==groupInfo.MaxMem{
		return ErrGroupFull	
	}

		
		//update the group member list
		//NOTE::maybe should check about the dupli err in cache before processing
	go func(groupID,userID uuid.UUID){
			s.groupCache.memMuLock.Lock();
			defer s.groupCache.memMuLock.Unlock();	
		memberLists := s.groupCache.MemberCache[groupID]
			*s.groupCache.MemberCache[groupID] = append(*memberLists,userID)
			
	}(groupID,userID)
		
	//update the group's metadata
	go func(groupID,userID uuid.UUID){
		s.groupCache.groupMuLock.Lock();
		defer s.groupCache.groupMuLock.Unlock()
		s.groupCache.GroupCache[groupID].TotalMem +=1;
	}(groupID,userID)
	

	//assign the job for the db operation of adding member
	s.mq.Publish("addGroupMember",&ManageGroupPublishStruct{
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
		//might need to consider moving this to somewhere
		log.Println("warning: hub channel is full message dropped")
	}
	return nil
}

//should separate the normal mem leaveGroup with leader leaveGroup
func (s *groupService)leaveGroup(ctx context.Context,groupID uuid.UUID,userID uuid.UUID)error{
	//saving into the db should be different from the join one
	leaveStruct := chatmodel.GroupActionInfo{
		GroupID: groupID,
		UserID: userID,
	}
	//firts update the group metadata first 
	go func(gpID *uuid.UUID){
	s.groupCache.groupMuLock.Lock()
	s.groupCache.GroupCache[*gpID].TotalMem -=1
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
			memberIdsList = memberIdsList[:len(memberIdsList)-1]
			log.Printf("memberIdsList value: %v",memberIdsList)
			return memberIdsList
		}
		log.Printf("finished updating in the cache : %v",updatedMemberIdsList())
		*s.groupCache.MemberCache[*gpID] = updatedMemberIdsList()
		
	}(&groupID,&userID)


	//and then we publish the job for the db worker to consume
	s.mq.Publish("removeGroupMember",&ManageGroupPublishStruct{
		GroupId: groupID,
		UserID: userID,
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


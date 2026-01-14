package groups

import (
	"context"
	"encoding/json"
	"log"
	"time"

	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

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
		log.Printf("failed to create the group #%s#",err)
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
			log.Printf("failed to create a creator: %v",err)
			chen.RetriesCount ++
		 s.mq.Republish(chen,chen.RetriesCount)		
			return	
		}
		log.Printf("Doned the group leader worker %v",chen.LocalTag)
	}	

}

//this will update the cache of the group and its member
func (s *groupService)StartWorkerForAddMemberList(channel chan *mq.Channel){
	context , cancel := context.WithTimeout(context.Background(),5*time.Second)
	defer cancel()
	for chen := range channel{
		//NOTE::this comes  in as memberstruct not bytes i don't have to check
		msg := chen.Msg.(membersPubStruct)
		go func(msg membersPubStruct){
			s.groupCache.groupMuLock.Lock()
			//plus 1 so that the group creator is also included
			if _,ok:= s.groupCache.GroupCache[msg.GroupId];!ok{
				s.groupCache.GroupCache[msg.GroupId] = &CacheGroupInfo{}
			}
			s.groupCache.GroupCache[msg.GroupId].totalMem =int16(len(msg.UserIds))+1
			log.Printf("total_mem in the cache #%v#",s.groupCache.GroupCache[msg.GroupId].totalMem)	
			s.groupCache.groupMuLock.Unlock()

		}(msg)

		go func(msg membersPubStruct){
			s.groupCache.memMuLock.Lock()	
			if _,ok:=s.groupCache.MemberCache[msg.GroupId];!ok{
				s.groupCache.MemberCache[msg.GroupId]= &[]uuid.UUID{}
			}
			s.groupCache.MemberCache[msg.GroupId] = &msg.UserIds
			log.Printf("members in the group #%v#",msg.UserIds)
		}(msg)
		
		//i could use the unest thing and just raw byte to send in one round trip 
		//but looping through a thousand members doesn't seem too slow at all so yeah
		var memberIds []database.AddMemberListParams
		for _,v:= range msg.UserIds{
			memberIds = append(memberIds,database.AddMemberListParams{
				 MemberID: v,
				 GroupID: msg.GroupId,
			})	
		}

		//this is done in one round trip using copyfrom
		err := s.groupRepo.addMemberList(context,&memberIds)
		if err !=nil{
			log.Printf("failed to create member: %v",err)
			chen.RetriesCount ++
		 s.mq.Republish(chen,chen.RetriesCount)		
			return	
		}
		log.Printf("Successfully added the group members")
		
	}	

}


func (s *groupService)StartWorkerForAddMember(channel chan *mq.Channel){
	for chen := range channel{
		msg := chen.Msg.(ManageGroupPublishStruct)		
		//the reason i didn't check the map and its existent is this endpoint will be only available when there is a group
		switch msg.Action{
		case "Join":

		}	

				
}
}

func (s *groupService)JoinGroup(msg *ManageGroupPublishStruct,chen chan *mq.Channel)error{
	go func(msg *ManageGroupPublishStruct){
			s.groupCache.groupMuLock.Lock()
			s.groupCache.GroupCache[msg.GroupId].totalMem +=1
			log.Printf("Finished incrementing the mem_coutn #%v#",s.groupCache.GroupCache[msg.GroupId].totalMem)
			s.groupCache.groupMuLock.Unlock()	

		}(msg)	

		go func(msg *ManageGroupPublishStruct){
			s.groupCache.memMuLock.Lock()
			memberList:=s.groupCache.MemberCache[msg.GroupId] 
			//this takes the value at the memory address and actually update the value in that address
			//you can just update append to the address value that's why you have to use * to get the value 
			*s.groupCache.MemberCache[msg.GroupId] = append(*memberList,msg.UserID)		
			log.Printf("members updating finished #%v#",s.groupCache.MemberCache[msg.GroupId])
			s.groupCache.memMuLock.Unlock()
		}(msg)	


		contex,cancel := context.WithTimeout(context.Background(),1*time.Second)
		defer cancel()
		err := s.groupRepo.addMember(contex,&database.AddMemberParams{
	GroupID: msg.GroupId,
	MemberID: msg.UserID,
		})	
		if err !=nil{
			log.Printf("failed to add member: %v",err)
			chen.RetriesCount ++
		 s.mq.Republish(chen,chen.RetriesCount)		
			return	nil
		}
		log.Printf("successfully added into the group")
	return nil		
}



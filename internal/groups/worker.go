package groups

import (
	"context"
	"log"
	"time"

	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
//	"github.com/google/uuid"
)

//NOTE::you really need centralized encoder and decoder
//there is a code duplication in this fucntion
//the channel will be blocked if there is no value in the channel
//so as long as they have value in it each worker will try to get that and 
//then process it
func (s *groupService)StartWorkerForCreateGroup(channel chan *mq.Channel){
	for chen := range channel{
		msg := chen.Msg.(GroupPublish)
		groupID := msg.GroupID.ChatID
		groupInfo := msg.GroupInfo
		err := s.groupRepo.createGroup(groupID,createGroupRequest{
			GroupName: groupInfo.GroupName,
			Description: groupInfo.Description,
			MaxMems: groupInfo.MaxMems ,
		})

		if err !=nil{
		log.Printf("failed to create the group #%s#",err)
			chen.RetriesCount ++
		 s.mq.Republish(chen,chen.RetriesCount)		
			continue
		}
		log.Printf("Successfully created the group")
	}	

}
func (s *groupService)StartWorkerForCreateGroupLeader(channel chan *mq.Channel){
	for chen := range channel{
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
			continue	
		}
		log.Printf("Doned the group leader worker %v",chen.LocalTag)
	}	

}

//worker should only have to worry about the i/o operation
func (s *groupService)StartWorkerForAddMemberList(channel chan *mq.Channel){
	context , cancel := context.WithTimeout(context.Background(),5*time.Second)
	defer cancel()
	for chen := range channel{
		//NOTE::this comes  in as memberstruct not bytes i don't have to check
		msg := chen.Msg.(membersPubStruct)
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
			continue
		}
		log.Printf("Successfully added the group members")
		
	}	

}


func (s *groupService)StartWorkerForAddMember(channel chan *mq.Channel){
	for chen := range channel{
		msg := chen.Msg.(ManageGroupPublishStruct)		
		//the reason i didn't check the map and its existent is this endpoint will be only available when there is a group
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
			continue	
		}
		log.Printf("successfully added into the group")
		}		
}


func (s *groupService)StartWorkerForLeaveMember(channel chan *mq.Channel){
	for  chen := range channel{
		msg := chen.Msg.(ManageGroupPublishStruct) 
		context,cancel := context.WithTimeout(context.Background(),1*time.Second)
		defer cancel();
		payload := &database.DeleteMemFromGroupParams{
			GroupID: msg.GroupId,
			MemberID: msg.UserID,	
		}
		err := s.groupRepo.deleteMember(context,payload)
		if err !=nil{
			log.Printf("failed to remove the member from the group %s",err)
			chen.RetriesCount ++
			s.mq.Republish(chen,chen.RetriesCount)
			continue
		}
		log.Printf("Successfully removed the member from the group")
	}
}

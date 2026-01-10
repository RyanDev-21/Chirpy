package groups

import (
	"context"
	"encoding/json"
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
}


//both join and leave share the same struct type
type groupService struct{
	groupRepo GroupRepo
	hub *chatmodel.Hub
	mq *mq.MainMQ
}



func NewGroupService(groupRepo GroupRepo,hub *chatmodel.Hub,mq *mq.MainMQ)GroupService{
	return &groupService{
		groupRepo: groupRepo,
		hub : hub,
		mq: mq,
	}
}

//get new chatID and store it in the db and return the groupInfo struct
func (s *groupService)createGroup(ctx context.Context,createrID uuid.UUID,groupInfo *createGroupRequest)(*GroupInfo,error){
	chatID, err := uuid.NewUUID()
	if err !=nil{
		return nil,err
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
	s.mq.Publish("createGroup",payload)


	return &GroupInfo{
		ChatID: chatID,
	},nil
}

//NOTE::you really need centralized encoder and decoder
//there is a code duplication in this fucntion
func (s *groupService)StartWorkerForCreateGroup(channel chan *mq.Channel){
	var retriesCount = make(map[int]int) 
	for chen := range channel{
		//if this is not the valid type then the pipeline will break
		jsonBytes := chen.Msg.([]byte)
		var msg GroupPublish
		 err := json.Unmarshal(jsonBytes,&msg)
		if err !=nil{
			if _,ok := retriesCount[chen.LocalTag];!ok{
				retriesCount[chen.LocalTag] =0	
			}
			retriesCount[chen.LocalTag] ++
		 s.mq.Republish(chen,retriesCount[chen.LocalTag])		

		}
		
		groupID := msg.GroupID.ChatID
		groupInfo := msg.GroupInfo
		err = s.groupRepo.createGroup(groupID,createGroupRequest{
			GroupName: groupInfo.GroupName,
			Description: groupInfo.Description,
			MaxMems: groupInfo.MaxMems ,
		})

		if err !=nil{
		 if _,ok := retriesCount[chen.LocalTag];!ok{
				retriesCount[chen.LocalTag] =0	
			}
			retriesCount[chen.LocalTag] ++
		 s.mq.Republish(chen,retriesCount[chen.LocalTag])		
		}
		log.Printf("Successfully created the group")
	}	

}


//might have to refactor these two service into one service which operate based on the type of the service
//send the joinStruct to the JoinChan to ativate the function of the hub
//TODO:right now haven't stored the generated groupID in db so we can basically add the invalid id and will still work
//need to fix that
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


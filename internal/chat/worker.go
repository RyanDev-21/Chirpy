package chat

import (
	"log"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

//parentId is string type from json
//we need to make it nil so that the db will not take this value
//NOTE::maybe there is a better way to do this 
func ParentIdIdentifier(parentID string)*uuid.UUID{
	var fakeID *uuid.UUID
	if parentID == ""{
		fakeID = nil
		return fakeID
	}
	*fakeID= uuid.MustParse(parentID)
	return fakeID
}

//this one will store the msg id and its info into db 
func (s *chatService)StartWorkerForAddPrivateMessage(channel chan *mq.Channel){
	for chen := range channel{
		msg := chen.Msg.(chatmodel.MessageMetaData)		
		parentID := ParentIdIdentifier(msg.MsgInfo.Msg.ParendID)
		//this one stores into message table
		_,err := s.chatRepo.AddMessagePrivate(&database.AddMessagePrivateParams{
			ID:msg.ID,
			Content:*GetStringType(msg.MsgInfo.Msg.Content),
			Parentid: *GetUUIDType(parentID),	
			FromID: *GetUUIDType(msg.MsgInfo.FromID),	
			ToID: *GetUUIDType(uuid.MustParse(msg.MsgInfo.Msg.ToID)),
		})
		if err !=nil{
			chen.RetriesCount++;
			s.mq.Republish(chen,chen.RetriesCount)
			continue
		}
		log.Printf("Successfully addded the message to the db")
	}	
}

func (s *chatService)StartWorkerForAddPublicMessage(channel chan *mq.Channel){
	for chen := range channel{
		msg := chen.Msg.(chatmodel.MessageMetaData)	
		parentID := ParentIdIdentifier(msg.MsgInfo.Msg.ParendID)	
		_,err := s.chatRepo.AddMessagePublic(&database.AddMessagePublicParams{
				ID: msg.ID,
				Content: *GetStringType(msg.MsgInfo.Msg.Content),
				ParentID: *GetUUIDType(parentID),
				GroupID: *GetUUIDType(msg.MsgInfo.Msg.ToID),
				FromID: *GetUUIDType(msg.MsgInfo.FromID),
			})
		if err !=nil{
			chen.RetriesCount ++
			s.mq.Republish(chen,chen.RetriesCount)
			continue
		}
		log.Printf("Successfully save the group message")
	}
}


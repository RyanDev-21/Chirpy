package chat

import (
	"log"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

//save into db
func (s *chatService)StartWorkerForAddMessage(channel chan *mq.Channel){
	for chen := range channel{

		msg := chen.Msg.(chatmodel.PublishMessageStruct)		
		var fakeID *uuid.UUID
		if msg.Msg.ParendID == ""{
			fakeID = nil
		}
		_,err := s.chatRepo.AddMessage(&database.AddMessageParams{
			Content:*GetStringType(msg.Msg.Content),
			Parentid: *GetUUIDType(*fakeID),	
			FromID: *GetUUIDType(msg.UserID),	
			ToID: *GetUUIDType(uuid.MustParse(msg.Msg.ToID)),
		})
		if err !=nil{
			chen.RetriesCount++;
			s.mq.Republish(chen,chen.RetriesCount)
			continue
		}
		log.Printf("Successfully addded the message to the db")
	}	
}


package chat

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/pkg/helper"
	"github.com/google/uuid"
)

func getChatKey(firstID,secondID uuid.UUID)string{
	if firstID.String()<secondID.String(){
		return fmt.Sprintf("%v_%v",firstID,secondID)
	}	
	return fmt.Sprintf("%v_%v",secondID,firstID)
}

func getPayload(msgId ,userID uuid.UUID,msg *chatmodel.Message)*chatmodel.MessageMetaData{ 
	return &chatmodel.MessageMetaData{
		ID: msgId,
		MsgInfo: &chatmodel.MessageCache{
			Msg: *msg,
			FromID: userID,
		},
	}
}

//gen the unique msgID and store in cache and db
//this one needs a parseID as the chatID need to generate and stuff
func handlePrivateMsg(clientID,parseID uuid.UUID,msg *chatmodel.Message,cache ChatRepoCache){
	context,cancel := context.WithTimeout(context.Background(),1*time.Second)
		defer cancel()
	//generate  for the chatID
		key := getChatKey(clientID,parseID)
		//has to generate the uuid for the messageId
		msgID,err := uuid.NewV7()	
		if err !=nil{
			log.Fatal("failed to get the uuidv7")
		}

		payload :=getPayload(msgID,clientID,msg)
		//first need to update the cache
		err=cache.AddMessage(context,key,payload)	
		if err !=nil{
		log.Fatal("failed to stroe into the cache \n #%s#",err)
	}
	
}

//gen msgID and store it in cache and group db
func handlePublicMsg(clientID uuid.UUID,msg *chatmodel.Message,cache ChatRepoCache){
	context, cancel:=context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	chatID :=msg.ToID
	msgID,err := uuid.NewV7()	
		if err !=nil{
			log.Fatal("failed to get the uuidv7")
		}
	payload :=getPayload(msgID,clientID,msg)
	err=cache.AddMessage(context,chatID,payload)	
		if err !=nil{
		log.Fatal("failed to stor into the cache \n #%s#",err)
	}
}
func publishJobHelper(job string,payload interface{},msgQ *mq.MainMQ){
	//dummy logger for now
	logger := slog.Default()
	context,cancel := context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	err:=msgQ.PublishWithContext(context,job,payload)	
	if err !=nil{
		helper.SaveIntoLog(job,payload,logger)	
	}

}

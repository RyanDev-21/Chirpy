package chatmodel

import (
	"context"
	"fmt"
	"log"
	"time"
	"github.com/google/uuid"
)

func GetChatKey(firstID,secondID uuid.UUID)string{
	if firstID.String()<secondID.String(){
		return fmt.Sprintf("%v_%v",firstID,secondID)
	}	
	return fmt.Sprintf("%v_%v",firstID,secondID)
}



//gen the unique msgID and store in cache and db
func HandlePrivateMsg(c *Client,msg *Message,parseID uuid.UUID){
	context,cancel := context.WithTimeout(context.Background(),1*time.Second)
		defer cancel()
	//generate  for the chatID
		key := GetChatKey(c.UserID,parseID)
		//has to generate the uuid for the messageId
		msgID,err := uuid.NewV7()	
		if err !=nil{
			log.Fatal("failed to get the uuidv7")
		}

		payload :=GetPayload(msgID,c.UserID,msg)
		//first need to update the cache
		err=c.Cache.AddMessage(context,key,payload)	
		if err !=nil{
		log.Fatal("failed to stroe into the cache \n #%s#",err)
	}
		//second publish the job
		c.MsgQ.Publish("addMessagePrivate",payload)	
	
}

//gen msgID and store it in cache and group db
func HandlePublicMsg(c *Client,msg *Message){
	context, cancel:=context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	chatID :=msg.ToID
	msgID,err := uuid.NewV7()	
		if err !=nil{
			log.Fatal("failed to get the uuidv7")
		}
	payload :=GetPayload(msgID,c.UserID,msg)
	err=c.Cache.AddMessage(context,chatID,payload)	
		if err !=nil{
		log.Fatal("failed to stroe into the cache \n #%s#",err)
	}
	c.MsgQ.Publish("addMessagePublic",payload)	
}

func GetPayload(msgId ,userID uuid.UUID,msg *Message)*MessageMetaData{ return &MessageMetaData{
		ID: msgId,
		MsgInfo: &MessageCache{
			Msg: *msg,
			FromID: userID,
		},
	}
}
